package api

import (
	"context"
	"errors"
	"strings"

	"opensight/internal/billing"
	"opensight/internal/billing/reconcile"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"

	connect "connectrpc.com/connect"
)

var _ opensightv1connect.BillingServiceHandler = (*Server)(nil)

// checkoutIntegrationIdentifier is STABLE, not per-request: Stripe uses it to
// group Checkout Sessions so their conversion is comparable in the dashboard
// (design 08). The 8-letter suffix was rolled once and committed — randomising
// it per session would produce a population of one and defeat the purpose.
const checkoutIntegrationIdentifier = "opensight-signup-vqmzhrtk"

// StartCheckout sends the calling tenant to a Stripe-hosted Checkout Session
// for the Starter plan, creating the tenant's permanent Stripe Customer on
// first use (design 08 "Checkout Session", "Customer").
func (s *Server) StartCheckout(ctx context.Context, _ *connect.Request[opensightv1.StartCheckoutRequest]) (*connect.Response[opensightv1.StartCheckoutResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "start checkout")
	if cerr != nil {
		return nil, cerr
	}

	sub, err := s.subscriptions.GetByTenant(ctx, su.TenantID)
	if err != nil {
		// Signup guarantees the row; a miss here is our bug, not the client's.
		return nil, s.rpcInternal("start checkout: get subscription", err)
	}

	// Second-checkout guard, before any Stripe call, so a double-checkout
	// costs zero API calls.
	if billing.DeriveAccess(sub.AccessState(), nowUTC()) == billing.AccessFull {
		return nil, rpcFailedPrecondition("this account already has an active subscription")
	}

	plan, err := billing.PlanFor(sub.PlanCode)
	if err != nil {
		return nil, s.rpcInternal("start checkout: resolve plan", err)
	}
	priceID := s.stripePriceIDs[plan.Code]
	if priceID == "" {
		// A misconfigured deploy (missing Stripe Price env var), not a client
		// fault.
		return nil, s.rpcInternal("start checkout: no price configured", errors.New("no stripe price id for plan "+plan.Code))
	}

	customerID := sub.StripeCustomerID
	if customerID == nil {
		cus, err := s.billing.CreateCustomer(ctx, billing.CreateCustomerParams{
			TenantID: su.TenantID.String(),
			Email:    su.Email,
		})
		if err != nil {
			return nil, s.rpcInternal("start checkout: create customer", err)
		}
		// The id that won, not necessarily cus.ID: a concurrent request may
		// already have installed a different Customer (write-once).
		won, err := s.subscriptions.SetStripeCustomerID(ctx, su.TenantID, cus.ID)
		if err != nil {
			return nil, s.rpcError("start checkout: set stripe customer id", err)
		}
		customerID = &won
	}

	// Plain concatenation, never url.Values.Encode(): the {CHECKOUT_SESSION_ID}
	// placeholder must reach Stripe unescaped. appBaseURL is already
	// normalized absolute with no trailing slash.
	session, err := s.billing.CreateCheckoutSession(ctx, billing.CreateCheckoutSessionParams{
		CustomerID:            *customerID,
		PriceID:               priceID,
		TenantID:              su.TenantID.String(),
		IntegrationIdentifier: checkoutIntegrationIdentifier,
		SuccessURL:            s.appBaseURL + "/checkout/return?session_id={CHECKOUT_SESSION_ID}",
		CancelURL:             s.appBaseURL + "/billing",
	})
	if err != nil {
		return nil, s.rpcInternal("start checkout: create checkout session", err)
	}

	return connect.NewResponse(&opensightv1.StartCheckoutResponse{CheckoutUrl: session.URL}), nil
}

// ConfirmCheckout retrieves a Checkout Session server-side, confirms it
// belongs to the calling tenant, and runs the same reconcile the webhook
// runs (design 08 "Checkout return") — so a customer who just paid is never
// told they haven't while a webhook is in flight.
func (s *Server) ConfirmCheckout(ctx context.Context, req *connect.Request[opensightv1.ConfirmCheckoutRequest]) (*connect.Response[opensightv1.ConfirmCheckoutResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "confirm checkout")
	if cerr != nil {
		return nil, cerr
	}

	sessionID := strings.TrimSpace(req.Msg.SessionId)
	if sessionID == "" {
		return nil, rpcInvalidArgument("session_id is required")
	}

	cs, err := s.billing.GetCheckoutSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, billing.ErrCheckoutSessionNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		return nil, s.rpcInternal("confirm checkout: get checkout session", err)
	}

	// Another tenant's session must be indistinguishable from one that never
	// existed — the identical NotFound, byte for byte, returned before any
	// write. A distinct code here would make ConfirmCheckout a session-id
	// oracle.
	if cs.ClientReferenceID != su.TenantID.String() {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}

	// Keyed off the row's own customer id, never cs.CustomerID: a second belt
	// alongside the check above, so even a forged session id could not cause
	// a write against another tenant's Customer. Reconcile runs
	// unconditionally, without branching on cs.Status — the session carries
	// identity, not truth. An abandoned (open) session reconciles to "no
	// subscription", the row is untouched, and the response is ACCESS_NEVER,
	// which is exactly right.
	sub, err := s.reconciler.Tenant(ctx, su.TenantID)
	if err != nil && !errors.Is(err, reconcile.ErrNoCustomer) {
		return nil, s.rpcInternal("confirm checkout: reconcile", err)
	}
	// ErrNoCustomer would mean the row lost its Customer id after this
	// session was created for this tenant, which StartCheckout's write-once
	// persistence makes structurally impossible — reconcile.Tenant still
	// returns the (unwritten) row alongside it, so access derives correctly
	// either way.

	access := billing.DeriveAccess(sub.AccessState(), nowUTC())
	return connect.NewResponse(&opensightv1.ConfirmCheckoutResponse{Access: accessToProto(access)}), nil
}

// CreatePortalSession starts a Stripe-hosted Customer Portal session for the
// calling tenant (design 08 "Customer Portal"). A tenant with no Stripe
// Customer is refused rather than sent somewhere broken — this correctly
// covers a comped tenant too, which has no Stripe objects at all (AC #1).
func (s *Server) CreatePortalSession(ctx context.Context, _ *connect.Request[opensightv1.CreatePortalSessionRequest]) (*connect.Response[opensightv1.CreatePortalSessionResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "create portal session")
	if cerr != nil {
		return nil, cerr
	}

	sub, err := s.subscriptions.GetByTenant(ctx, su.TenantID)
	if err != nil {
		// Signup guarantees the row; a miss here is our bug, not the client's.
		return nil, s.rpcInternal("create portal session: get subscription", err)
	}
	if sub.StripeCustomerID == nil {
		return nil, rpcFailedPrecondition("this account has no billing history yet")
	}

	session, err := s.billing.CreatePortalSession(ctx, billing.CreatePortalSessionParams{
		CustomerID:      *sub.StripeCustomerID,
		ConfigurationID: s.stripePortalConfigurationID,
		ReturnURL:       s.appBaseURL + "/billing",
	})
	if err != nil {
		return nil, s.rpcInternal("create portal session: create portal session", err)
	}

	return connect.NewResponse(&opensightv1.CreatePortalSessionResponse{PortalUrl: session.URL}), nil
}
