package stripe

import (
	"errors"
	"fmt"
	"testing"

	stripesdk "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"opensight/internal/billing"
)

const testWebhookSecret = "whsec_test_secret"

// signedTestEvent signs body with testWebhookSecret exactly as Stripe signs a
// live delivery, returning the payload and its Stripe-Signature header.
func signedTestEvent(t *testing.T, body string) (payload []byte, header string) {
	t.Helper()
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: []byte(body),
		Secret:  testWebhookSecret,
	})
	return signed.Payload, signed.Header
}

// testEventJSON builds a minimal, API-version-compatible event payload
// (ConstructEvent rejects a mismatched api_version) with the given customer
// value spliced in verbatim, so callers can pass a bare string ("cus_123",
// already JSON-quoted by the caller) or an expanded object.
func testEventJSON(id, eventType, customerJSON string) string {
	return fmt.Sprintf(`{
  "id": %q,
  "object": "event",
  "api_version": %q,
  "type": %q,
  "data": {"object": {"customer": %s}}
}`, id, stripesdk.APIVersion, eventType, customerJSON)
}

// TestWebhookVerifierValidSignature covers the AC that a verified delivery
// parses id/type/customer — the identity the handler and reconcile need.
func TestWebhookVerifierValidSignature(t *testing.T) {
	body := testEventJSON("evt_123", "customer.subscription.updated", `"cus_abc"`)
	payload, header := signedTestEvent(t, body)

	v := NewWebhookVerifier(testWebhookSecret)
	event, err := v.Verify(payload, header)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if event.ID != "evt_123" {
		t.Errorf("ID = %q, want evt_123", event.ID)
	}
	if event.Type != "customer.subscription.updated" {
		t.Errorf("Type = %q, want customer.subscription.updated", event.Type)
	}
	if event.CustomerID != "cus_abc" {
		t.Errorf("CustomerID = %q, want cus_abc", event.CustomerID)
	}
}

// TestWebhookVerifierExpandedCustomer covers the AC that an expanded
// {"id": ...} customer object still yields the id, not just the bare-string
// shape.
func TestWebhookVerifierExpandedCustomer(t *testing.T) {
	body := testEventJSON("evt_124", "customer.subscription.deleted", `{"id": "cus_expanded", "object": "customer"}`)
	payload, header := signedTestEvent(t, body)

	v := NewWebhookVerifier(testWebhookSecret)
	event, err := v.Verify(payload, header)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if event.CustomerID != "cus_expanded" {
		t.Errorf("CustomerID = %q, want cus_expanded", event.CustomerID)
	}
}

// TestWebhookVerifierNoCustomer covers a payload with no customer field at
// all yielding CustomerID == "", not an error.
func TestWebhookVerifierNoCustomer(t *testing.T) {
	body := fmt.Sprintf(`{"id": "evt_125", "object": "event", "api_version": %q, "type": "checkout.session.completed", "data": {"object": {}}}`, stripesdk.APIVersion)
	payload, header := signedTestEvent(t, body)

	v := NewWebhookVerifier(testWebhookSecret)
	event, err := v.Verify(payload, header)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if event.CustomerID != "" {
		t.Errorf("CustomerID = %q, want empty", event.CustomerID)
	}
}

// TestWebhookVerifierTamperedBody covers the AC that a delivery failing
// signature verification is rejected: the signature was computed over the
// original body, so a body edited after signing must fail.
func TestWebhookVerifierTamperedBody(t *testing.T) {
	body := testEventJSON("evt_126", "customer.subscription.updated", `"cus_abc"`)
	payload, header := signedTestEvent(t, body)
	tampered := append([]byte{}, payload...)
	tampered = []byte(string(tampered) + " ")

	v := NewWebhookVerifier(testWebhookSecret)
	if _, err := v.Verify(tampered, header); !errors.Is(err, billing.ErrInvalidSignature) {
		t.Fatalf("Verify(tampered) = %v, want ErrInvalidSignature", err)
	}
}

// TestWebhookVerifierEmptySecretRejectsEverything covers the AC that an
// empty STRIPE_WEBHOOK_SECRET is not a bypass: a stub-mode deploy that
// somehow builds a real verifier must reject every delivery, valid signature
// or not.
func TestWebhookVerifierEmptySecretRejectsEverything(t *testing.T) {
	body := testEventJSON("evt_127", "customer.subscription.updated", `"cus_abc"`)
	// Signed against a real secret the verifier does not have.
	payload, header := signedTestEvent(t, body)

	v := NewWebhookVerifier("")
	if _, err := v.Verify(payload, header); !errors.Is(err, billing.ErrInvalidSignature) {
		t.Fatalf("Verify with empty secret = %v, want ErrInvalidSignature", err)
	}
}
