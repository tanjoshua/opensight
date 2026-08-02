package billing

// This file holds the plain-Go parameter and result types every Stripe
// consumer speaks. They live here, not in internal/billing/stripe, so the
// narrow seams that internal/api and internal/billing/reconcile declare stay
// free of stripe-go — preserving this package's dependency-free promise
// (catalog.go) for internal/store, which imports it for the plan catalog.

import (
	"errors"
	"time"
)

// ErrNoSubscription is returned by GetSubscriptionForCustomer when a Stripe
// Customer exists but has never completed a checkout. Reconcile needs to tell "no subscription" from "call failed"; a zero value would not.
var ErrNoSubscription = errors.New("no subscription for customer")

// ErrCheckoutSessionNotFound is returned by GetCheckoutSession for a session
// id that does not exist — the checkout-session equivalent of ErrNoSubscription.
var ErrCheckoutSessionNotFound = errors.New("checkout session not found")

// CreateCustomerParams creates a Stripe Customer for a tenant (design 08
// "Customer" — one Customer per tenant, created lazily and never recreated).
type CreateCustomerParams struct {
	TenantID string
	Email    string
}

// Customer is the subset of a Stripe Customer callers need.
type Customer struct {
	ID string
}

// CreateCheckoutSessionParams builds a `mode: subscription` Checkout Session
// (design 08 "Checkout Session"). StartCheckout owns computing these; the
// provider owns mapping them onto Stripe.
type CreateCheckoutSessionParams struct {
	CustomerID string
	PriceID    string
	TenantID   string
	// IntegrationIdentifier is a stable label with a random suffix (e.g.
	// opensight-signup-vqmzhrtk) so checkout performance is comparable in
	// the dashboard.
	IntegrationIdentifier string
	SuccessURL            string
	CancelURL             string
}

// CheckoutSession mirrors the subset of a Stripe Checkout Session callers
// need. Status is verbatim Stripe (open/complete/expired), the same rule as
// subscriptions.stripe_status (design 08).
type CheckoutSession struct {
	ID                string
	URL               string
	Status            string
	CustomerID        string
	SubscriptionID    string
	ClientReferenceID string
}

// Subscription mirrors the subset of a Stripe Subscription callers need.
// Status is verbatim Stripe; CurrentPeriodEnd is the zero time when unknown
// (a subscription response with no items).
type Subscription struct {
	ID                string
	CustomerID        string
	Status            string
	CurrentPeriodEnd  time.Time
	CancelAtPeriodEnd bool
}

// CreatePortalSessionParams starts a Customer Portal session (design 08
// "Customer Portal").
type CreatePortalSessionParams struct {
	CustomerID string
	ReturnURL  string
	// ConfigurationID pins the session to the repo-owned Billing Portal
	// Configuration (internal/billing/portal.go) rather than the
	// account default. The Stripe adapter requires it; test fakes may ignore it.
	ConfigurationID string
}

// PortalSession is the subset of a Stripe Billing Portal Session callers need.
type PortalSession struct {
	URL string
}

// Price mirrors the subset of a Stripe Price callers need for display.
// Interval is empty when the Price has no Recurring component —
// tolerated, not a construction error, mirroring subscriptionFromStripe's
// nil-tolerant mapping.
type Price struct {
	UnitAmount int64
	Currency   string
	Interval   string
}
