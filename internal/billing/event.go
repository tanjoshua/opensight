package billing

import "errors"

// Event is the plain-Go projection of a verified Stripe webhook event
// (design 08 "Webhook"). Only identity survives the boundary — CustomerID,
// not subscription state — because the handler re-fetches the subscription
// from the API rather than trusting the payload.
type Event struct {
	ID   string
	Type string
	// CustomerID is the Stripe Customer id the event concerns, extracted from
	// the event payload's own "customer" field (bare string or expanded
	// object, whichever the event carries). Empty when the event's object has
	// no customer at all.
	CustomerID string
}

// WebhookVerifier verifies a raw webhook delivery against its signature and
// parses out the identity fields reconcile needs. Implemented by
// internal/billing/stripe so internal/api never
// imports stripe-go directly (the same reason Provider is split out).
type WebhookVerifier interface {
	Verify(payload []byte, signatureHeader string) (Event, error)
}

// ErrInvalidSignature is returned by WebhookVerifier.Verify for a payload
// that fails signature verification — including, deliberately, every payload
// when the verifier was built with an empty secret, so a stub-mode deploy
// can never be tricked into accepting an unsigned delivery.
var ErrInvalidSignature = errors.New("invalid webhook signature")
