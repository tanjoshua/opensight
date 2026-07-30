package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"opensight/internal/store"
)

// maxStripeWebhookBodyBytes caps the request body a webhook delivery may
// send. Generous relative to a typical Stripe event (a few KB), but bounded
// so a malformed or hostile request can't exhaust memory before signature
// verification even runs.
const maxStripeWebhookBodyBytes = 1 << 20 // 1 MiB

// handledStripeEventTypes are the four subscription lifecycle events design
// 08 subscribes to. Invoice events are deliberately not handled — Stripe's
// own emails cover receipts and dunning notices, and every state that
// matters is reachable from the subscription itself.
var handledStripeEventTypes = map[string]bool{
	"checkout.session.completed":    true,
	"customer.subscription.created": true,
	"customer.subscription.updated": true,
	"customer.subscription.deleted": true,
}

// handleStripeWebhook is Stripe's delivery endpoint (design 08 "Webhook"), a
// chi route outside /rpc — the request's Stripe-Signature header is its only
// authentication, verified against s.webhooks before anything is persisted.
//
// Response codes are deliberate: 400 for a verification failure, 200 for
// anything handled or intentionally ignored, and 500 only when reconcile
// failed so Stripe retries. Deliveries are not persisted or deduplicated:
// reconciliation fetches Stripe's current state and asserts local desired
// state, making duplicate notifications harmless.
func (s *Server) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStripeWebhookBodyBytes))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid request body", "could not read request body")
		return
	}

	event, err := s.webhooks.Verify(body, r.Header.Get("Stripe-Signature"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid signature", "webhook signature verification failed")
		return
	}

	if !handledStripeEventTypes[event.Type] || event.CustomerID == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if _, err := s.reconciler.ByCustomer(ctx, event.CustomerID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// An orphan Customer from the accepted crash window (design 08
			// "Customer") — not a failure Stripe should retry forever.
			slog.Warn("api: stripe webhook: no subscription for customer", "event_id", event.ID, "customer_id", event.CustomerID)
			w.WriteHeader(http.StatusOK)
			return
		}
		slog.Error("api: stripe webhook: reconcile", "event_id", event.ID, "customer_id", event.CustomerID, "error", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", "failed to reconcile subscription")
		return
	}

	w.WriteHeader(http.StatusOK)
}
