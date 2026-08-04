package api

import (
	"reflect"
	"sort"
	"testing"

	"opensight/internal/billing"
)

// TestHandledEventsMatchDesiredWebhookEvents keeps handledStripeEventTypes
// (what the running server accepts) and billing.DesiredWebhookEvents (what
// `opensight stripe webhook-config` provisions Stripe to send) in lockstep —
// a change to either without the other must fail CI, not surface as silently
// dropped or rejected events in production.
func TestHandledEventsMatchDesiredWebhookEvents(t *testing.T) {
	handled := make([]string, 0, len(handledStripeEventTypes))
	for event := range handledStripeEventTypes {
		handled = append(handled, event)
	}
	sort.Strings(handled)

	desired := append([]string(nil), billing.DesiredWebhookEvents...)
	sort.Strings(desired)

	if !reflect.DeepEqual(handled, desired) {
		t.Fatalf("handledStripeEventTypes = %v, billing.DesiredWebhookEvents = %v; must match exactly", handled, desired)
	}
}
