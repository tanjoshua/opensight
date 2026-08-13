package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"opensight/internal/billing"
	"opensight/internal/billing/reconcile"
	"opensight/internal/billing/stripe"
	"opensight/internal/domain"
	"opensight/internal/store"

	stripesdk "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"github.com/jackc/pgx/v5/pgxpool"
)

const webhookTestSecret = "whsec_integration_test"

type webhookProvider struct {
	subscriptions map[string]billing.Subscription
}

func newWebhookProvider(customerID string) *webhookProvider {
	return &webhookProvider{subscriptions: map[string]billing.Subscription{
		customerID: {
			ID:               "sub_" + customerID,
			CustomerID:       customerID,
			Status:           "active",
			CurrentPeriodEnd: time.Now().Add(30 * 24 * time.Hour),
		},
	}}
}

func (p *webhookProvider) GetSubscriptionForCustomer(_ context.Context, customerID string) (billing.Subscription, error) {
	sub, ok := p.subscriptions[customerID]
	if !ok {
		return billing.Subscription{}, billing.ErrNoSubscription
	}
	return sub, nil
}

func (p *webhookProvider) setStatus(customerID, status string) {
	sub := p.subscriptions[customerID]
	sub.Status = status
	p.subscriptions[customerID] = sub
}

// mustDomainID generates a fresh UUIDv7, the shape accounts.id/businesses.id
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

	accountID := mustDomainID(t)
	businessID := mustDomainID(t)
	customerID := "cus_" + accountID.String()
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})
	if _, err := db.Exec(ctx, "INSERT INTO accounts (id, name, slug) VALUES ($1, 'Out Of Order Account', $2)", accountID, "test-"+accountID.String()); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	pool := db
	businesses := store.New(pool)
	category := "clinic"
	activatedAt := nowUTC()
	if _, err := businesses.CreateBusiness(ctx, store.CreateBusinessParams{
		ID: businessID, AccountID: accountID, Status: store.BusinessStatusActive, Name: "Acme Clinic",
		Category:    &category,
		Services:    json.RawMessage(`["checkups"]`),
		Location:    json.RawMessage(`{"address":"1 Road","area":"Central","city":"Singapore","country":"SG"}`),
		ActivatedAt: &activatedAt,
	}); err != nil {
		t.Fatalf("CreateBusiness: %v", err)
	}

	subscriptions := store.New(pool)
	subscriptionID := "sub_" + accountID.String()
	activeStatus := "active"
	if err := subscriptions.Upsert(ctx, store.UpsertSubscriptionParams{
		AccountID: accountID, PlanCode: billing.Starter.Code,
		StripeCustomerID: &customerID, StripeSubscriptionID: &subscriptionID, StripeStatus: &activeStatus,
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	provider := newWebhookProvider(customerID)
	provider.setStatus(customerID, "canceled")

	reconciler := reconcile.New(subscriptions, provider, nil, subscriptions, nil)

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

	sub, err := subscriptions.GetByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("GetByAccount after deleted delivery: %v", err)
	}
	if sub.StripeStatus == nil || *sub.StripeStatus != "canceled" {
		t.Fatalf("stripe_status after deleted delivery = %v, want canceled", sub.StripeStatus)
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

	sub, err = subscriptions.GetByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("GetByAccount after stale updated delivery: %v", err)
	}
	if sub.StripeStatus == nil || *sub.StripeStatus != "canceled" {
		t.Fatalf("stripe_status after stale updated delivery = %v, want still canceled (not resurrected)", sub.StripeStatus)
	}

}

// TestStripeWebhookLapseAndReactivationPreservesData is BILL-10's AC4 proof
// (design 08 "Lapse and reactivation"): a customer.subscription.deleted
// delivery pauses monitoring and drops access to lapsed without touching a
// single business/prompt/run row, and a later customer.subscription.updated
// delivery that finds the subscription active again unpauses monitoring and
// restores full access, still against the same rows and the same Stripe
// customer id — reactivation is a status flip, never a re-signup.
func TestStripeWebhookLapseAndReactivationPreservesData(t *testing.T) {
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

	accountID := mustDomainID(t)
	businessID := mustDomainID(t)
	customerID := "cus_" + accountID.String()
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})
	if _, err := db.Exec(ctx, "INSERT INTO accounts (id, name, slug) VALUES ($1, 'Out Of Order Account', $2)", accountID, "test-"+accountID.String()); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	pool := db
	businesses := store.New(pool)
	category := "clinic"
	activatedAt := nowUTC()
	if _, err := businesses.CreateBusiness(ctx, store.CreateBusinessParams{
		ID: businessID, AccountID: accountID, Status: store.BusinessStatusActive, Name: "Acme Clinic",
		Category:    &category,
		Services:    json.RawMessage(`["checkups"]`),
		Location:    json.RawMessage(`{"address":"1 Road","area":"Central","city":"Singapore","country":"SG"}`),
		ActivatedAt: &activatedAt,
	}); err != nil {
		t.Fatalf("CreateBusiness: %v", err)
	}

	subscriptions := store.New(pool)
	subscriptionID := "sub_" + accountID.String()
	activeStatus := "active"
	if err := subscriptions.Upsert(ctx, store.UpsertSubscriptionParams{
		AccountID: accountID, PlanCode: billing.Starter.Code,
		StripeCustomerID: &customerID, StripeSubscriptionID: &subscriptionID, StripeStatus: &activeStatus,
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	// Seed a prompt and an analyzed run so there is real data for the lapse
	// to leave untouched.
	prompts := store.New(pool)
	prompt, err := prompts.CreateActivePrompt(ctx, store.CreateActivePromptParams{
		AccountID: accountID, BusinessID: businessID, Text: "Who are the best clinics?",
	})
	if err != nil {
		t.Fatalf("seed prompt: %v", err)
	}

	runs := store.New(pool)
	run, err := runs.UpsertRun(ctx, accountID, store.UpsertRunParams{
		BusinessID: businessID, Platform: store.PlatformChatGPT, Trigger: store.RunTriggerInitial,
		ScheduledFor: activatedAt, JobID: 105, ExpectedResults: 1, Spec: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}

	provider := newWebhookProvider(customerID)

	reconciler := reconcile.New(subscriptions, provider, nil, subscriptions, nil)

	srv := New(Deps{
		Reconciler: reconciler,
		Webhooks:   stripe.NewWebhookVerifier(webhookTestSecret),
	})

	// businessRow/promptRow/runRow snapshot every mutable-looking field, so a
	// later comparison catches an accidental write from either delivery, not
	// just a status change.
	businessRow := func(t *testing.T) store.Business {
		t.Helper()
		b, err := businesses.GetBusiness(ctx, accountID, businessID)
		if err != nil {
			t.Fatalf("GetBusiness: %v", err)
		}
		return b
	}
	promptRow := func(t *testing.T) store.Prompt {
		t.Helper()
		p, err := prompts.GetPrompt(ctx, accountID, prompt.ID)
		if err != nil {
			t.Fatalf("GetPrompt: %v", err)
		}
		return p
	}
	runRow := func(t *testing.T) store.RunListItem {
		t.Helper()
		list, err := runs.ListRuns(ctx, accountID, businessID)
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		for _, r := range list {
			if r.ID == run.ID {
				return r
			}
		}
		t.Fatalf("run %s not found after delivery", run.ID)
		return store.RunListItem{}
	}

	businessBefore, promptBefore, runBefore := businessRow(t), promptRow(t), runRow(t)

	// First delivery: customer.subscription.deleted — pauses monitoring and
	// drops access to lapsed, touching only the subscriptions row.
	provider.setStatus(customerID, "canceled")
	deletedPayload, deletedHeader := signWebhookEvent(t, "evt_lapse_1", "customer.subscription.deleted", customerID, "canceled")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(deletedPayload))
	req.Header.Set("Stripe-Signature", deletedHeader)
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deleted delivery status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	sub, err := subscriptions.GetByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("GetByAccount after deleted delivery: %v", err)
	}
	if access := billing.DeriveAccess(sub.AccessState(), nowUTC()); access != billing.AccessLapsed {
		t.Fatalf("access after deleted delivery = %v, want lapsed", access)
	}
	if !reflect.DeepEqual(businessBefore, businessRow(t)) {
		t.Fatalf("business row changed after lapse")
	}
	if !reflect.DeepEqual(promptBefore, promptRow(t)) {
		t.Fatalf("prompt row changed after lapse")
	}
	if !reflect.DeepEqual(runBefore, runRow(t)) {
		t.Fatalf("run row changed after lapse")
	}

	// Second delivery: the subscription is live again — unpauses monitoring
	// and restores full access, still against the same rows and customer id.
	provider.setStatus(customerID, "active")
	updatedPayload, updatedHeader := signWebhookEvent(t, "evt_lapse_2", "customer.subscription.updated", customerID, "active")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(updatedPayload))
	req.Header.Set("Stripe-Signature", updatedHeader)
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("updated delivery status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	sub, err = subscriptions.GetByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("GetByAccount after updated delivery: %v", err)
	}
	if access := billing.DeriveAccess(sub.AccessState(), nowUTC()); access != billing.AccessFull {
		t.Fatalf("access after updated delivery = %v, want full", access)
	}
	if sub.StripeCustomerID == nil || *sub.StripeCustomerID != customerID {
		t.Fatalf("stripe_customer_id after reactivation = %v, want %s (reactivation is a status flip, not a new customer)", sub.StripeCustomerID, customerID)
	}
	if !reflect.DeepEqual(businessBefore, businessRow(t)) {
		t.Fatalf("business row changed after reactivation")
	}
	if !reflect.DeepEqual(promptBefore, promptRow(t)) {
		t.Fatalf("prompt row changed after reactivation")
	}
	if !reflect.DeepEqual(runBefore, runRow(t)) {
		t.Fatalf("run row changed after reactivation")
	}
}
