package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"opensight/internal/billing"
	"opensight/internal/billing/reconcile"
	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
)

// newBillingTestServer builds a Server wired for direct BillingService method
// calls: subs doubles as both the subscriptions seam and reconcile's
// subscription store, so reconcile.Reconciler runs for real over the same
// fake the handler sees (design 08 "exactly one code path").
type billingTestProvider interface {
	billingProvider
	GetSubscriptionForCustomer(ctx context.Context, customerID string) (billing.Subscription, error)
}

func newBillingTestServer(subs *fakeSubscriptionStore, provider billingTestProvider) *Server {
	srv := &Server{
		subscriptions:               subs,
		billing:                     provider,
		reconciler:                  reconcile.New(subs, provider, nil, nil, nil),
		stripePriceIDs:              map[string]string{billing.Starter.Code: "price_stub_starter"},
		appBaseURL:                  "https://app.example.com",
		stripePortalConfigurationID: "bpc_test",
	}
	srv.priceCache.prices = make(map[string]billing.Price)
	return srv
}

// billingRPCContext returns a context carrying a session user for tenantID,
// for direct method calls that bypass the session interceptor.
func billingRPCContext(t *testing.T, tenantID domain.ID) context.Context {
	t.Helper()
	return withSessionUser(context.Background(), store.SessionUser{
		UserID:     mustHashV7(t, userID),
		TenantID:   tenantID,
		Email:      "customer@example.com",
		TenantName: "Acme Clinic",
		ExpiresAt:  time.Now().Add(time.Hour),
	})
}

// countingProvider wraps the test billing client to count CreateCustomer calls, so
// tests can assert the second-checkout guard trips before any Stripe call
// (case 2) and that an existing Customer is reused rather than recreated
// (case 3).
type countingProvider struct {
	billingTestProvider
	createCustomerCalls        int
	createCheckoutSessionCalls int
}

func (c *countingProvider) CreateCustomer(ctx context.Context, params billing.CreateCustomerParams) (billing.Customer, error) {
	c.createCustomerCalls++
	return c.billingTestProvider.CreateCustomer(ctx, params)
}

func (c *countingProvider) CreateCheckoutSession(ctx context.Context, params billing.CreateCheckoutSessionParams) (billing.CheckoutSession, error) {
	c.createCheckoutSessionCalls++
	return c.billingTestProvider.CreateCheckoutSession(ctx, params)
}

type portalCapturingProvider struct {
	billingTestProvider
	params billing.CreatePortalSessionParams
}

func (p *portalCapturingProvider) CreatePortalSession(_ context.Context, params billing.CreatePortalSessionParams) (billing.PortalSession, error) {
	p.params = params
	return billing.PortalSession{URL: params.ReturnURL}, nil
}

// sessionIDFromCheckoutURL extracts the session_id StartCheckout's stub URL
// carries, mirroring what the SPA's /checkout/return route would read off
// its own query string.
func sessionIDFromCheckoutURL(t *testing.T, checkoutURL string) string {
	t.Helper()
	_, sessionID, ok := strings.Cut(checkoutURL, "session_id=")
	if !ok || sessionID == "" {
		t.Fatalf("checkout url %q has no session_id", checkoutURL)
	}
	return sessionID
}

// TestBillingCheckoutFunnel covers BILL-4's core AC: the Customer is created
// once and persisted before the checkout URL is returned, the URL is the
// stub's substituted success URL, and feeding its session_id back into
// ConfirmCheckout yields ACCESS_FULL with the row carrying a subscription id
// and status active — reconcile exercised end to end, not separately.
func TestBillingCheckoutFunnel(t *testing.T) {
	tenantID := uuid.New()
	subs := &fakeSubscriptionStore{sub: store.Subscription{TenantID: tenantID, PlanCode: billing.Starter.Code}}
	srv := newBillingTestServer(subs, billing.NewStubProvider())
	ctx := billingRPCContext(t, tenantID)

	startResp, err := srv.StartCheckout(ctx, connect.NewRequest(&opensightv1.StartCheckoutRequest{}))
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	if subs.setCustomerIDCalls != 1 {
		t.Fatalf("setCustomerIDCalls = %d, want 1", subs.setCustomerIDCalls)
	}
	if subs.sub.StripeCustomerID == nil || *subs.sub.StripeCustomerID == "" {
		t.Fatal("stripe_customer_id was not persisted before the checkout url was returned")
	}
	const wantPrefix = "https://app.example.com/checkout/return?session_id=cs_stub_"
	if !strings.HasPrefix(startResp.Msg.CheckoutUrl, wantPrefix) {
		t.Fatalf("checkout url = %q, want prefix %q", startResp.Msg.CheckoutUrl, wantPrefix)
	}
	sessionID := sessionIDFromCheckoutURL(t, startResp.Msg.CheckoutUrl)

	confirmResp, err := srv.ConfirmCheckout(ctx, connect.NewRequest(&opensightv1.ConfirmCheckoutRequest{SessionId: sessionID}))
	if err != nil {
		t.Fatalf("ConfirmCheckout: %v", err)
	}
	if confirmResp.Msg.Access != opensightv1.Access_ACCESS_FULL {
		t.Fatalf("access = %v, want ACCESS_FULL", confirmResp.Msg.Access)
	}
	if subs.sub.StripeSubscriptionID == nil || *subs.sub.StripeSubscriptionID == "" {
		t.Fatal("reconcile did not write a stripe_subscription_id")
	}
	if subs.sub.StripeStatus == nil || *subs.sub.StripeStatus != "active" {
		t.Fatalf("stripe_status = %v, want active", subs.sub.StripeStatus)
	}
}

// TestStartCheckoutRefusesSecondCheckout covers the AC that a customer
// already paying cannot start a second checkout, and that the guard trips
// before any provider call (a double-checkout must cost zero API calls).
func TestStartCheckoutRefusesSecondCheckout(t *testing.T) {
	tenantID := uuid.New()
	subs := &fakeSubscriptionStore{sub: store.Subscription{TenantID: tenantID, PlanCode: billing.Starter.Code}}
	counting := &countingProvider{billingTestProvider: billing.NewStubProvider()}
	srv := newBillingTestServer(subs, counting)
	ctx := billingRPCContext(t, tenantID)

	startResp, err := srv.StartCheckout(ctx, connect.NewRequest(&opensightv1.StartCheckoutRequest{}))
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	sessionID := sessionIDFromCheckoutURL(t, startResp.Msg.CheckoutUrl)
	if _, err := srv.ConfirmCheckout(ctx, connect.NewRequest(&opensightv1.ConfirmCheckoutRequest{SessionId: sessionID})); err != nil {
		t.Fatalf("ConfirmCheckout: %v", err)
	}

	_, err = srv.StartCheckout(ctx, connect.NewRequest(&opensightv1.StartCheckoutRequest{}))
	if err == nil {
		t.Fatal("second StartCheckout succeeded, want CodeFailedPrecondition")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("second StartCheckout code = %v, want CodeFailedPrecondition", connect.CodeOf(err))
	}
	if counting.createCustomerCalls != 1 {
		t.Fatalf("createCustomerCalls = %d, want 1 (second call must not reach the provider)", counting.createCustomerCalls)
	}
	if counting.createCheckoutSessionCalls != 1 {
		t.Fatalf("createCheckoutSessionCalls = %d, want 1 (second call must not reach the provider)", counting.createCheckoutSessionCalls)
	}
}

// TestStartCheckoutRefusesRecoverableSubscription protects the lapsed case:
// access alone must not decide checkout eligibility. An existing unpaid
// subscription is managed in the portal, not duplicated with a new Checkout.
func TestStartCheckoutRefusesRecoverableSubscription(t *testing.T) {
	tenantID := uuid.New()
	customerID := "cus_existing"
	subscriptionID := "sub_existing"
	status := "unpaid"
	subs := &fakeSubscriptionStore{sub: store.Subscription{
		TenantID:             tenantID,
		PlanCode:             billing.Starter.Code,
		StripeCustomerID:     &customerID,
		StripeSubscriptionID: &subscriptionID,
		StripeStatus:         &status,
	}}
	counting := &countingProvider{billingTestProvider: billing.NewStubProvider()}
	srv := newBillingTestServer(subs, counting)

	_, err := srv.StartCheckout(billingRPCContext(t, tenantID), connect.NewRequest(&opensightv1.StartCheckoutRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("StartCheckout code = %v, want CodeFailedPrecondition", connect.CodeOf(err))
	}
	if counting.createCustomerCalls != 0 || counting.createCheckoutSessionCalls != 0 {
		t.Fatalf("provider calls = customer %d, checkout %d; want none", counting.createCustomerCalls, counting.createCheckoutSessionCalls)
	}
}

// TestStartCheckoutReusesExistingCustomer covers the AC that the Customer is
// permanent: a tenant that already has one must never get a second.
func TestStartCheckoutReusesExistingCustomer(t *testing.T) {
	tenantID := uuid.New()
	existing := "cus_existing"
	subs := &fakeSubscriptionStore{sub: store.Subscription{
		TenantID: tenantID, PlanCode: billing.Starter.Code, StripeCustomerID: &existing,
	}}
	counting := &countingProvider{billingTestProvider: billing.NewStubProvider()}
	srv := newBillingTestServer(subs, counting)
	ctx := billingRPCContext(t, tenantID)

	if _, err := srv.StartCheckout(ctx, connect.NewRequest(&opensightv1.StartCheckoutRequest{})); err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	if counting.createCustomerCalls != 0 {
		t.Fatalf("createCustomerCalls = %d, want 0 (existing customer reused)", counting.createCustomerCalls)
	}
	if subs.setCustomerIDCalls != 0 {
		t.Fatalf("setCustomerIDCalls = %d, want 0", subs.setCustomerIDCalls)
	}
}

// TestConfirmCheckoutRejectsOtherTenantsSession covers the AC that a checkout
// session belonging to another tenant can never be confirmed: tenant B
// confirming tenant A's session id gets the identical NotFound an unknown
// session id gets, and tenant B's row is left unwritten.
func TestConfirmCheckoutRejectsOtherTenantsSession(t *testing.T) {
	provider := billing.NewStubProvider()

	tenantA := uuid.New()
	subsA := &fakeSubscriptionStore{sub: store.Subscription{TenantID: tenantA, PlanCode: billing.Starter.Code}}
	srvA := newBillingTestServer(subsA, provider)
	startResp, err := srvA.StartCheckout(billingRPCContext(t, tenantA), connect.NewRequest(&opensightv1.StartCheckoutRequest{}))
	if err != nil {
		t.Fatalf("StartCheckout (tenant A): %v", err)
	}
	sessionID := sessionIDFromCheckoutURL(t, startResp.Msg.CheckoutUrl)

	tenantB := uuid.New()
	subsB := &fakeSubscriptionStore{sub: store.Subscription{TenantID: tenantB, PlanCode: billing.Starter.Code}}
	srvB := newBillingTestServer(subsB, provider)

	_, err = srvB.ConfirmCheckout(billingRPCContext(t, tenantB), connect.NewRequest(&opensightv1.ConfirmCheckoutRequest{SessionId: sessionID}))
	if err == nil {
		t.Fatal("ConfirmCheckout across tenants succeeded, want CodeNotFound")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want CodeNotFound", connect.CodeOf(err))
	}
	if len(subsB.upsertCalls) != 0 {
		t.Fatalf("tenant B's row was written (%d upsert calls), want untouched", len(subsB.upsertCalls))
	}
}

// TestConfirmCheckoutUnknownSessionID covers a session id that was never
// created (typo, replay of a stale link) getting the same NotFound as a
// cross-tenant session, not a distinct error that would make ConfirmCheckout
// an oracle.
func TestConfirmCheckoutUnknownSessionID(t *testing.T) {
	tenantID := uuid.New()
	subs := &fakeSubscriptionStore{sub: store.Subscription{TenantID: tenantID, PlanCode: billing.Starter.Code}}
	srv := newBillingTestServer(subs, billing.NewStubProvider())

	_, err := srv.ConfirmCheckout(billingRPCContext(t, tenantID), connect.NewRequest(&opensightv1.ConfirmCheckoutRequest{SessionId: "cs_unknown"}))
	if err == nil {
		t.Fatal("ConfirmCheckout with an unknown session id succeeded, want CodeNotFound")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want CodeNotFound", connect.CodeOf(err))
	}
}

// TestCreatePortalSession covers BILL-8's happy path: a tenant with a Stripe
// Customer reaches the portal, with the return URL pointed at /billing (the
// same target ConfirmCheckout and StartCheckout use).
func TestCreatePortalSession(t *testing.T) {
	tenantID := uuid.New()
	customerID := "cus_existing"
	subs := &fakeSubscriptionStore{sub: store.Subscription{
		TenantID: tenantID, PlanCode: billing.Starter.Code, StripeCustomerID: &customerID,
	}}
	provider := &portalCapturingProvider{billingTestProvider: billing.NewStubProvider()}
	srv := newBillingTestServer(subs, provider)

	resp, err := srv.CreatePortalSession(billingRPCContext(t, tenantID), connect.NewRequest(&opensightv1.CreatePortalSessionRequest{}))
	if err != nil {
		t.Fatalf("CreatePortalSession: %v", err)
	}
	if resp.Msg.PortalUrl != "https://app.example.com/billing" {
		t.Fatalf("PortalUrl = %q, want https://app.example.com/billing", resp.Msg.PortalUrl)
	}
	if provider.params.ConfigurationID != "bpc_test" {
		t.Fatalf("ConfigurationID = %q, want bpc_test", provider.params.ConfigurationID)
	}
}

// TestCreatePortalSessionRefusesNoCustomer covers the AC that a tenant with
// no Stripe Customer is refused rather than sent somewhere broken — this is
// also the path a comped tenant takes, since comped tenants have no Stripe
// objects at all (design 08 "Customer Portal").
func TestCreatePortalSessionRefusesNoCustomer(t *testing.T) {
	tenantID := uuid.New()
	subs := &fakeSubscriptionStore{sub: store.Subscription{TenantID: tenantID, PlanCode: billing.Starter.Code}}
	srv := newBillingTestServer(subs, billing.NewStubProvider())

	_, err := srv.CreatePortalSession(billingRPCContext(t, tenantID), connect.NewRequest(&opensightv1.CreatePortalSessionRequest{}))
	if err == nil {
		t.Fatal("CreatePortalSession with no Stripe Customer succeeded, want CodeFailedPrecondition")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want CodeFailedPrecondition", connect.CodeOf(err))
	}
}

// TestGetBilling covers BILL-9's core AC across every access-relevant
// billing state: access, plan, verbatim status passthrough, live price
// display, and the renewal-or-end date pair
// (current_period_end + cancel_at_period_end), mirroring the access table in
// design 08 rather than re-deriving it.
func TestGetBilling(t *testing.T) {
	periodEnd := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	activeSub := "sub_1"
	activeStatus := "active"
	canceledStatus := "canceled"

	tests := []struct {
		name           string
		sub            store.Subscription
		wantAccess     opensightv1.Access
		wantAction     opensightv1.BillingAction
		wantComped     bool
		wantStatus     string
		wantCancelling bool
		wantPeriodEnd  bool
	}{
		{
			name:       "never paid",
			sub:        store.Subscription{PlanCode: billing.Starter.Code},
			wantAccess: opensightv1.Access_ACCESS_NEVER,
			wantAction: opensightv1.BillingAction_BILLING_ACTION_CHECKOUT,
		},
		{
			name: "full, active",
			sub: store.Subscription{
				PlanCode:             billing.Starter.Code,
				StripeSubscriptionID: &activeSub,
				StripeStatus:         &activeStatus,
				CurrentPeriodEnd:     &periodEnd,
			},
			wantAccess:    opensightv1.Access_ACCESS_FULL,
			wantAction:    opensightv1.BillingAction_BILLING_ACTION_PORTAL,
			wantStatus:    "active",
			wantPeriodEnd: true,
		},
		{
			name: "lapsed, cancelling at period end",
			sub: store.Subscription{
				PlanCode:             billing.Starter.Code,
				StripeSubscriptionID: &activeSub,
				StripeStatus:         &canceledStatus,
				CurrentPeriodEnd:     &periodEnd,
				CancelAtPeriodEnd:    true,
			},
			wantAccess:     opensightv1.Access_ACCESS_LAPSED,
			wantAction:     opensightv1.BillingAction_BILLING_ACTION_CHECKOUT,
			wantStatus:     "canceled",
			wantCancelling: true,
			wantPeriodEnd:  true,
		},
		{
			name:       "comped",
			sub:        store.Subscription{PlanCode: billing.Starter.Code, Comped: true},
			wantAccess: opensightv1.Access_ACCESS_FULL,
			wantAction: opensightv1.BillingAction_BILLING_ACTION_NONE,
			wantComped: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			tc.sub.TenantID = tenantID
			subs := &fakeSubscriptionStore{sub: tc.sub}
			srv := newBillingTestServer(subs, billing.NewStubProvider())

			resp, err := srv.GetBilling(billingRPCContext(t, tenantID), connect.NewRequest(&opensightv1.GetBillingRequest{}))
			if err != nil {
				t.Fatalf("GetBilling: %v", err)
			}
			if resp.Msg.Access != tc.wantAccess {
				t.Fatalf("Access = %v, want %v", resp.Msg.Access, tc.wantAccess)
			}
			if resp.Msg.Action != tc.wantAction {
				t.Fatalf("Action = %v, want %v", resp.Msg.Action, tc.wantAction)
			}
			if resp.Msg.Comped != tc.wantComped {
				t.Fatalf("Comped = %v, want %v", resp.Msg.Comped, tc.wantComped)
			}
			if resp.Msg.StripeStatus != tc.wantStatus {
				t.Fatalf("StripeStatus = %q, want %q", resp.Msg.StripeStatus, tc.wantStatus)
			}
			if resp.Msg.CancelAtPeriodEnd != tc.wantCancelling {
				t.Fatalf("CancelAtPeriodEnd = %v, want %v", resp.Msg.CancelAtPeriodEnd, tc.wantCancelling)
			}
			if tc.wantPeriodEnd {
				if resp.Msg.CurrentPeriodEnd == nil || !resp.Msg.CurrentPeriodEnd.AsTime().Equal(periodEnd) {
					t.Fatalf("CurrentPeriodEnd = %v, want %v", resp.Msg.CurrentPeriodEnd, periodEnd)
				}
			} else if resp.Msg.CurrentPeriodEnd != nil {
				t.Fatalf("CurrentPeriodEnd = %v, want nil", resp.Msg.CurrentPeriodEnd)
			}
			if resp.Msg.Plan == nil || resp.Msg.Plan.Code != billing.Starter.Code {
				t.Fatalf("Plan = %v, want code %q", resp.Msg.Plan, billing.Starter.Code)
			}
			// Every state, including comped and never-paid, resolves a live
			// price: display is a plan-catalog fact, not a Stripe-object
			// fact, and a comped tenant's plan is still sold at this price.
			if resp.Msg.PriceUnitAmount == 0 || resp.Msg.PriceCurrency == "" || resp.Msg.PriceInterval == "" {
				t.Fatalf("price fields not populated: %+v", resp.Msg)
			}
		})
	}
}

// TestGetBillingCachesPrice covers the process-lifetime price cache: a
// second GetBilling call for the same plan must not hit the provider again.
func TestGetBillingCachesPrice(t *testing.T) {
	tenantID := uuid.New()
	subs := &fakeSubscriptionStore{sub: store.Subscription{TenantID: tenantID, PlanCode: billing.Starter.Code}}
	provider := &countingPriceProvider{billingTestProvider: billing.NewStubProvider()}
	srv := newBillingTestServer(subs, provider)
	ctx := billingRPCContext(t, tenantID)

	if _, err := srv.GetBilling(ctx, connect.NewRequest(&opensightv1.GetBillingRequest{})); err != nil {
		t.Fatalf("GetBilling (1): %v", err)
	}
	if _, err := srv.GetBilling(ctx, connect.NewRequest(&opensightv1.GetBillingRequest{})); err != nil {
		t.Fatalf("GetBilling (2): %v", err)
	}
	if provider.getPriceCalls != 1 {
		t.Fatalf("getPriceCalls = %d, want 1 (second GetBilling must hit the cache)", provider.getPriceCalls)
	}
}

type countingPriceProvider struct {
	billingTestProvider
	getPriceCalls int
}

func (c *countingPriceProvider) GetPrice(ctx context.Context, priceID string) (billing.Price, error) {
	c.getPriceCalls++
	return c.billingTestProvider.GetPrice(ctx, priceID)
}
