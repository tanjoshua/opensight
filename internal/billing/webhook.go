package billing

// DesiredWebhookEvents is the exact set of Stripe event types the production
// webhook endpoint should be subscribed to (design 08 "Webhook"), applied by
// `opensight stripe webhook-config`. It must match
// internal/api/billing_webhook.go's handledStripeEventTypes exactly —
// TestHandledEventsMatchDesiredWebhookEvents in internal/api fails if either
// list changes without the other.
var DesiredWebhookEvents = []string{
	"checkout.session.completed",
	"customer.subscription.created",
	"customer.subscription.updated",
	"customer.subscription.deleted",
}
