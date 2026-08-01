package stripe

import (
	"encoding/json"
	"fmt"

	"github.com/stripe/stripe-go/v86/webhook"

	"opensight/internal/billing"
)

// WebhookVerifier verifies and parses Stripe webhook deliveries (design 08
// "Webhook"), the real implementation of billing.WebhookVerifier.
type WebhookVerifier struct {
	secret string
}

var _ billing.WebhookVerifier = (*WebhookVerifier)(nil)

// NewWebhookVerifier builds a WebhookVerifier for secret, the endpoint's
// Stripe signing secret. An empty secret is accepted at construction (a stub
// deploy never builds one) but Verify then rejects every delivery: a blank
// STRIPE_WEBHOOK_SECRET must never make the runtime silently
// accept unsigned events.
func NewWebhookVerifier(secret string) *WebhookVerifier {
	return &WebhookVerifier{secret: secret}
}

// Verify checks payload's signature against signatureHeader and extracts the
// event identity. Any failure — bad signature, empty secret, unparseable
// payload — collapses to billing.ErrInvalidSignature: the caller (the
// webhook handler) treats every verification failure identically, a 400 with
// nothing persisted.
func (v *WebhookVerifier) Verify(payload []byte, signatureHeader string) (billing.Event, error) {
	if v.secret == "" {
		return billing.Event{}, billing.ErrInvalidSignature
	}

	event, err := webhook.ConstructEvent(payload, signatureHeader, v.secret)
	if err != nil {
		return billing.Event{}, fmt.Errorf("%w: %v", billing.ErrInvalidSignature, err)
	}

	out := billing.Event{ID: event.ID, Type: string(event.Type)}
	if event.Data != nil {
		out.CustomerID = customerIDFromRaw(event.Data.Raw)
	}
	return out, nil
}

// customerIDFromRaw extracts the "customer" field from a Stripe event
// object's raw JSON, tolerating both shapes Stripe sends: a bare customer id
// string, or an expanded {"id": "..."} object. A payload with no customer
// field, or one that matches neither shape, yields "".
func customerIDFromRaw(raw json.RawMessage) string {
	var withCustomer struct {
		Customer json.RawMessage `json:"customer"`
	}
	if err := json.Unmarshal(raw, &withCustomer); err != nil || len(withCustomer.Customer) == 0 {
		return ""
	}

	var bare string
	if err := json.Unmarshal(withCustomer.Customer, &bare); err == nil {
		return bare
	}

	var expanded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(withCustomer.Customer, &expanded); err == nil {
		return expanded.ID
	}

	return ""
}
