package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/store"
)

// fakeWebhookVerifier is a scripted billing.WebhookVerifier: returns event
// unless err is set, and records the payload/header it was called with.
type fakeWebhookVerifier struct {
	event   billing.Event
	err     error
	calls   int
	payload []byte
	header  string
}

func (f *fakeWebhookVerifier) Verify(payload []byte, signatureHeader string) (billing.Event, error) {
	f.calls++
	f.payload = payload
	f.header = signatureHeader
	if f.err != nil {
		return billing.Event{}, f.err
	}
	return f.event, nil
}

// fakeWebhookReconciler is a scripted billingReconciler: only ByCustomer
// matters for the webhook, but Tenant must exist to satisfy the interface.
type fakeWebhookReconciler struct {
	sub           store.Subscription
	err           error
	byCustomerIDs []string
}

func (f *fakeWebhookReconciler) Tenant(context.Context, domain.ID) (store.Subscription, error) {
	return f.sub, f.err
}

func (f *fakeWebhookReconciler) ByCustomer(_ context.Context, customerID string) (store.Subscription, error) {
	f.byCustomerIDs = append(f.byCustomerIDs, customerID)
	return f.sub, f.err
}

// newWebhookTestServer builds a Server wired for direct requests against
// /webhooks/stripe.
func newWebhookTestServer(webhooks *fakeWebhookVerifier, reconciler *fakeWebhookReconciler) *Server {
	return &Server{
		webhooks:   webhooks,
		reconciler: reconciler,
	}
}

func postWebhook(srv *Server, body string, signature string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewBufferString(body))
	if signature != "" {
		req.Header.Set("Stripe-Signature", signature)
	}
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

// TestStripeWebhookBadSignatureRejected covers the AC that a delivery
// failing signature verification is rejected before reconciliation.
func TestStripeWebhookBadSignatureRejected(t *testing.T) {
	webhooks := &fakeWebhookVerifier{err: billing.ErrInvalidSignature}
	reconciler := &fakeWebhookReconciler{}
	srv := newWebhookTestServer(webhooks, reconciler)

	rec := postWebhook(srv, `{}`, "t=1,v1=bad")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(reconciler.byCustomerIDs) != 0 {
		t.Fatalf("ByCustomer called %d times, want 0", len(reconciler.byCustomerIDs))
	}
}

// TestStripeWebhookDuplicateDeliveryReconcilesAgain documents the deliberately
// simple idempotency model: a duplicate notification fetches and asserts
// Stripe's current desired state again.
func TestStripeWebhookDuplicateDeliveryReconcilesAgain(t *testing.T) {
	webhooks := &fakeWebhookVerifier{event: billing.Event{ID: "evt_1", Type: "customer.subscription.updated", CustomerID: "cus_1"}}
	reconciler := &fakeWebhookReconciler{}
	srv := newWebhookTestServer(webhooks, reconciler)

	first := postWebhook(srv, `{"id":"evt_1"}`, "t=1,v1=ok")
	second := postWebhook(srv, `{"id":"evt_1"}`, "t=1,v1=ok")

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d/%d, want 200/200", first.Code, second.Code)
	}
	if len(reconciler.byCustomerIDs) != 2 {
		t.Fatalf("ByCustomer called %d times, want 2", len(reconciler.byCustomerIDs))
	}
}

// TestStripeWebhookUnhandledTypeIgnored covers the AC that anything
// unrecognised is accepted and ignored.
func TestStripeWebhookUnhandledTypeIgnored(t *testing.T) {
	webhooks := &fakeWebhookVerifier{event: billing.Event{ID: "evt_2", Type: "invoice.paid", CustomerID: "cus_1"}}
	reconciler := &fakeWebhookReconciler{}
	srv := newWebhookTestServer(webhooks, reconciler)

	rec := postWebhook(srv, `{"id":"evt_2"}`, "t=1,v1=ok")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(reconciler.byCustomerIDs) != 0 {
		t.Fatalf("ByCustomer called %d times, want 0", len(reconciler.byCustomerIDs))
	}
}

// TestStripeWebhookNoCustomerIgnored covers a handled type with no customer
// id in the payload: there is nothing to reconcile by.
func TestStripeWebhookNoCustomerIgnored(t *testing.T) {
	webhooks := &fakeWebhookVerifier{event: billing.Event{ID: "evt_3", Type: "customer.subscription.updated", CustomerID: ""}}
	reconciler := &fakeWebhookReconciler{}
	srv := newWebhookTestServer(webhooks, reconciler)

	rec := postWebhook(srv, `{"id":"evt_3"}`, "t=1,v1=ok")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(reconciler.byCustomerIDs) != 0 {
		t.Fatalf("ByCustomer called %d times, want 0", len(reconciler.byCustomerIDs))
	}
}

// TestStripeWebhookReconcileFailureRetries covers the AC that Stripe retries
// when reconciliation genuinely failed.
func TestStripeWebhookReconcileFailureRetries(t *testing.T) {
	webhooks := &fakeWebhookVerifier{event: billing.Event{ID: "evt_4", Type: "customer.subscription.updated", CustomerID: "cus_1"}}
	reconciler := &fakeWebhookReconciler{err: errors.New("boom")}
	srv := newWebhookTestServer(webhooks, reconciler)

	rec := postWebhook(srv, `{"id":"evt_4"}`, "t=1,v1=ok")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

// TestStripeWebhookUnknownCustomerOK covers the AC that an orphan Customer
// (store.ErrNotFound from reconcile) is accepted rather than retried forever.
func TestStripeWebhookUnknownCustomerOK(t *testing.T) {
	webhooks := &fakeWebhookVerifier{event: billing.Event{ID: "evt_5", Type: "customer.subscription.updated", CustomerID: "cus_orphan"}}
	reconciler := &fakeWebhookReconciler{err: store.ErrNotFound}
	srv := newWebhookTestServer(webhooks, reconciler)

	rec := postWebhook(srv, `{"id":"evt_5"}`, "t=1,v1=ok")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestStripeWebhookHandledEventReconciles is the affirmative case.
func TestStripeWebhookHandledEventReconciles(t *testing.T) {
	webhooks := &fakeWebhookVerifier{event: billing.Event{ID: "evt_6", Type: "customer.subscription.deleted", CustomerID: "cus_1"}}
	reconciler := &fakeWebhookReconciler{}
	srv := newWebhookTestServer(webhooks, reconciler)

	rec := postWebhook(srv, `{"id":"evt_6"}`, "t=1,v1=ok")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(reconciler.byCustomerIDs) != 1 || reconciler.byCustomerIDs[0] != "cus_1" {
		t.Fatalf("byCustomerIDs = %v, want [cus_1]", reconciler.byCustomerIDs)
	}
}
