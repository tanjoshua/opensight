package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"opensight/internal/billing"
	"opensight/internal/billing/reconcile"
	"opensight/internal/billing/stripe"
	"opensight/internal/domain"
	"opensight/internal/store"

	stripesdk "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
)

const webhookTestSecret = "whsec_integration_test"

// mustDomainID generates a fresh UUIDv7, the shape tenants.id/businesses.id
// require (a check constraint, not merely a UNIQUE column).
func mustDomainID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("new domain id: %v", err)
	}
	return id
}

// signWebhookEvent builds a minimal, API-version-compatible Stripe event
// JSON (type, customer id, and — deliberately, to prove the handler never
// trusts it — a "status" the real subscription may not actually have), and
// signs it with webhookTestSecret exactly as Stripe signs a live delivery.
// GenerateTestSignedPayload is the SDK's own test helper (webhook package);
// no hand-rolled HMAC signer is needed.
func signWebhookEvent(t *testing.T, id, eventType, customerID, claimedStatus string) (payload []byte, header string) {
	t.Helper()
	body := fmt.Sprintf(
		`{"id":%q,"object":"event","api_version":%q,"type":%q,"data":{"object":{"customer":%q,"status":%q}}}`,
		id, stripesdk.APIVersion, eventType, customerID, claimedStatus,
	)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: []byte(body),
		Secret:  webhookTestSecret,
	})
	return signed.Payload, signed.Header
}

// TestStripeWebhookOutOfOrderDeliveryCannotResurrect is BILL-5's out-of-order
// proof (design 08, AC "Out-of-order delivery cannot resurrect a dead
// subscription"): a customer.subscription.deleted delivery cancels the
// subscription and pauses monitoring; a customer.subscription.updated
// delivered afterwards, whose own payload claims "active", must not move the
// row backward — reconcile always re-fetches Stripe's current state rather
// than trusting the payload, and the stub's current state is still canceled.
// The schedule must have been paused exactly once across both deliveries,
// not paused again and not unpaused by the stale update.
func TestStripeWebhookOutOfOrderDeliveryCannotResurrect(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run api integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	tenantID := mustDomainID(t)
	businessID := mustDomainID(t)
	customerID := "cus_" + tenantID.String()
	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query001, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query002, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query003, tenantID)
	})
	if _, err := testdb.Exec(ctx, db, testdb.Query004, tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	pool := db
	businesses := store.NewBusinessStore(pool)
	category := "clinic"
	activatedAt := nowUTC()
	if _, err := businesses.CreateBusiness(ctx, store.CreateBusinessParams{
		ID: businessID, TenantID: tenantID, Status: store.BusinessStatusActive, Name: "Acme Clinic",
		Category:    &category,
		Services:    json.RawMessage(`["checkups"]`),
		Location:    json.RawMessage(`{"address":"1 Road","area":"Central","city":"Singapore","country":"SG"}`),
		ActivatedAt: &activatedAt,
	}); err != nil {
		t.Fatalf("CreateBusiness: %v", err)
	}

	subscriptions := store.NewSubscriptionStore(pool)
	subscriptionID := "sub_" + tenantID.String()
	activeStatus := "active"
	if err := subscriptions.Upsert(ctx, store.UpsertSubscriptionParams{
		TenantID: tenantID, PlanCode: billing.Starter.Code,
		StripeCustomerID: &customerID, StripeSubscriptionID: &subscriptionID, StripeStatus: &activeStatus,
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	provider := billing.NewStubProvider()
	// Seed the stub's own subscription record for this customer (mirrors
	// what an earlier checkout would have done), then drive it to canceled —
	// the state a customer.subscription.deleted webhook reflects.
	if _, err := provider.CreateCheckoutSession(ctx, billing.CreateCheckoutSessionParams{
		CustomerID: customerID, SuccessURL: "https://app.example.com/checkout/return",
	}); err != nil {
		t.Fatalf("seed stub checkout session: %v", err)
	}
	provider.SetSubscriptionStatus(customerID, "canceled")

	temporal := &fakeTemporalClient{}
	monitoring := reconcile.NewMonitoring(businesses, temporal)
	reconciler := reconcile.New(subscriptions, provider, monitoring, store.NewAdvisoryLocker(pool), nil)

	srv := New(Deps{
		Reconciler: reconciler,
		Webhooks:   stripe.NewWebhookVerifier(webhookTestSecret),
	})

	// First delivery: customer.subscription.deleted. The event payload's own
	// claimed status is irrelevant to reconcile (it re-fetches from the
	// provider), so it is left "canceled" here too for clarity.
	deletedPayload, deletedHeader := signWebhookEvent(t, "evt_ooo_1", "customer.subscription.deleted", customerID, "canceled")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(deletedPayload))
	req.Header.Set("Stripe-Signature", deletedHeader)
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deleted delivery status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	sub, err := subscriptions.GetByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetByTenant after deleted delivery: %v", err)
	}
	if sub.StripeStatus == nil || *sub.StripeStatus != "canceled" {
		t.Fatalf("stripe_status after deleted delivery = %v, want canceled", sub.StripeStatus)
	}
	if len(temporal.schedule.pauses) != 1 {
		t.Fatalf("pauses after deleted delivery = %v, want exactly 1", temporal.schedule.pauses)
	}

	// Second delivery: a stale customer.subscription.updated claiming
	// "active" in its own payload. The stub's real state is still canceled
	// (nothing drove it back to active), so reconcile must leave the row
	// canceled and must not resume the schedule.
	updatedPayload, updatedHeader := signWebhookEvent(t, "evt_ooo_2", "customer.subscription.updated", customerID, "active")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(updatedPayload))
	req.Header.Set("Stripe-Signature", updatedHeader)
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stale updated delivery status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	sub, err = subscriptions.GetByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetByTenant after stale updated delivery: %v", err)
	}
	if sub.StripeStatus == nil || *sub.StripeStatus != "canceled" {
		t.Fatalf("stripe_status after stale updated delivery = %v, want still canceled (not resurrected)", sub.StripeStatus)
	}

	if len(temporal.schedule.pauses) != 1 {
		t.Fatalf("pauses across both deliveries = %v, want exactly 1 (not paused twice)", temporal.schedule.pauses)
	}
	if len(temporal.schedule.unpauses) != 0 {
		t.Fatalf("unpauses across both deliveries = %v, want 0 (the stale update must not resume monitoring)", temporal.schedule.unpauses)
	}

}
