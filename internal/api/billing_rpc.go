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
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ opensightv1connect.BillingServiceHandler = (*Server)(nil)

// checkoutIntegrationIdentifier is STABLE, not per-request: Stripe uses it to
// group Checkout Sessions so their conversion is comparable in the dashboard
// (design 08). The 8-letter suffix was rolled once and committed — randomising
// it per session would produce a population of one and defeat the purpose.
const checkoutIntegrationIdentifier = "opensight-signup-vqmzhrtk"

// GetBilling returns the calling tenant's billing state for the billing page
// (design 08 "RPC surface"): derived access, plan entitlements, the
// verbatim Stripe status, a live price for display, and the renewal-or-end
// date pair (current_period_end + cancel_at_period_end).
func (s *Server) GetBilling(ctx context.Context, _ *connect.Request[opensightv1.GetBillingRequest]) (*connect.Response[opensightv1.GetBillingResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get billing")
	if cerr != nil {
		return nil, cerr
	}

	sub, err := s.store.GetByTenant(ctx, su.TenantID)
	if err != nil {
		// Signup guarantees the row; a miss here is our bug, not the client's.
		return nil, s.rpcInternal("get billing: get subscription", err)
	}

	plan, err := billing.PlanFor(sub.PlanCode)
	if err != nil {
		return nil, s.rpcInternal("get billing: resolve plan", err)
	}

	resp := &opensightv1.GetBillingResponse{
		Access:            accessToProto(billing.DeriveAccess(sub.AccessState(), nowUTC())),
		Action:            billingActionToProto(billing.DeriveAction(sub.AccessState())),
		Plan:              planToProto(plan),
		Comped:            sub.Comped,
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
	}
	if sub.StripeStatus != nil {
		resp.StripeStatus = *sub.StripeStatus
	}
	if sub.CurrentPeriodEnd != nil {
		resp.CurrentPeriodEnd = timestamppb.New(*sub.CurrentPeriodEnd)
	}

	priceID := s.stripePriceIDs[plan.Code]
	if priceID == "" {
		// A misconfigured deploy (missing Stripe Price env var), not a client
		// fault.
		return nil, s.rpcInternal("get billing: no price configured", errors.New("no stripe price id for plan "+plan.Code))
	}
	price, err := s.cachedPrice(ctx, priceID)
	if err != nil {
		return nil, s.rpcInternal("get billing: get price", err)
	}
	resp.PriceUnitAmount = price.UnitAmount
	resp.PriceCurrency = price.Currency
	resp.PriceInterval = price.Interval

	return connect.NewResponse(resp), nil
}

// cachedPrice returns priceID's Stripe Price, fetching once per process
// lifetime: Stripe Prices are immutable (a new amount is a new Price
// object), so a cache entry can never disagree with what a customer is
// charged.
func (s *Server) cachedPrice(ctx context.Context, priceID string) (billing.Price, error) {
	s.priceCache.mu.Lock()
	if price, ok := s.priceCache.prices[priceID]; ok {
		s.priceCache.mu.Unlock()
		return price, nil
	}
	s.priceCache.mu.Unlock()

	price, err := s.billing.GetPrice(ctx, priceID)
	if err != nil {
		return billing.Price{}, err
	}

	s.priceCache.mu.Lock()
	s.priceCache.prices[priceID] = price
	s.priceCache.mu.Unlock()
	return price, nil
}

// StartCheckout sends the calling tenant to a Stripe-hosted Checkout Session
// for the Starter plan, creating the tenant's permanent Stripe Customer on
// first use (design 08 "Checkout Session", "Customer").
func (s *Server) StartCheckout(ctx context.Context, _ *connect.Request[opensightv1.StartCheckoutRequest]) (*connect.Response[opensightv1.StartCheckoutResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "start checkout")
	if cerr != nil {
		return nil, cerr
	}

	sub, err := s.store.GetByTenant(ctx, su.TenantID)
	if err != nil {
		// Signup guarantees the row; a miss here is our bug, not the client's.
		return nil, s.rpcInternal("start checkout: get subscription", err)
	}

	// Enforce the same server-derived action returned by GetBilling before any
	// Stripe call. Recoverable subscriptions belong in the Customer Portal;
	// Checkout is only for a tenant with no subscription or a terminal one.
	if billing.DeriveAction(sub.AccessState()) != billing.ActionCheckout {
		return nil, rpcFailedPrecondition("this account already has a subscription; manage it from the billing portal")
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
		won, err := s.store.SetStripeCustomerID(ctx, su.TenantID, cus.ID)
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

	sub, err := s.store.GetByTenant(ctx, su.TenantID)
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
