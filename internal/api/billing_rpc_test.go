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
func newBillingTestServer(subs *fakeSubscriptionStore, provider billing.Provider) *Server {
	return &Server{
		subscriptions:  subs,
		billing:        provider,
		reconciler:     reconcile.New(subs, provider, nil, nil, nil),
		stripePriceIDs: map[string]string{billing.Starter.Code: "price_stub_starter"},
		appBaseURL:     "https://app.example.com",
	}
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

// countingProvider wraps a billing.Provider to count CreateCustomer calls, so
// tests can assert the second-checkout guard trips before any Stripe call
// (case 2) and that an existing Customer is reused rather than recreated
// (case 3).
type countingProvider struct {
	billing.Provider
	createCustomerCalls int
}

func (c *countingProvider) CreateCustomer(ctx context.Context, params billing.CreateCustomerParams) (billing.Customer, error) {
	c.createCustomerCalls++
	return c.Provider.CreateCustomer(ctx, params)
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
	counting := &countingProvider{Provider: billing.NewStubProvider()}
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
}

// TestStartCheckoutReusesExistingCustomer covers the AC that the Customer is
// permanent: a tenant that already has one must never get a second.
func TestStartCheckoutReusesExistingCustomer(t *testing.T) {
	tenantID := uuid.New()
	existing := "cus_existing"
	subs := &fakeSubscriptionStore{sub: store.Subscription{
		TenantID: tenantID, PlanCode: billing.Starter.Code, StripeCustomerID: &existing,
	}}
	counting := &countingProvider{Provider: billing.NewStubProvider()}
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
