package billing

// PortalConfig is the desired Billing Portal Configuration, recorded as code
// rather than left as a Stripe dashboard default. Its settings are a
// behavioural contract the product depends on ("cancellation enabled, plan
// switching disabled"), not just an id to paste into .env — the same trap
// past_due_since (access.go) exists to guard against for dunning.
type PortalConfig struct {
	PaymentMethodUpdate bool
	InvoiceHistory      bool
	SubscriptionCancel  bool // at period end — design 08 "Lapse and reactivation"
	SubscriptionUpdate  bool // plan switching: one plan, so an empty switcher is worse than none
	DefaultReturnURL    string
}

// DesiredPortalConfig is the configuration every environment's portal is
// pinned to. DefaultReturnURL is deliberately left unset here: it is filled
// from APP_BASE_URL + "/billing" at apply time, since sandbox and live differ.
var DesiredPortalConfig = PortalConfig{
	PaymentMethodUpdate: true,
	InvoiceHistory:      true,
	SubscriptionCancel:  true,
	SubscriptionUpdate:  false,
}
