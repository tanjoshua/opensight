package billing

import (
	"context"
	"errors"
	"time"
)

// Provider is the seam between OpenSight and Stripe (design 08 "Stripe
// integration — Client"), the same shape as the PromptRunner adapter (01).
// It is implemented by StubProvider (this package, BILLING_PROVIDER=stub)
// and by the real adapter in internal/billing/stripe (BILLING_PROVIDER=stripe).
//
// Only plain Go types appear in the interface — no stripe-go import — so
// this package's dependency-free promise (catalog.go) extends to the
// provider seam too.
type Provider interface {
	CreateCustomer(ctx context.Context, params CreateCustomerParams) (Customer, error)
	CreateCheckoutSession(ctx context.Context, params CreateCheckoutSessionParams) (CheckoutSession, error)
	GetCheckoutSession(ctx context.Context, sessionID string) (CheckoutSession, error)
	GetSubscriptionForCustomer(ctx context.Context, customerID string) (Subscription, error)
	CreatePortalSession(ctx context.Context, params CreatePortalSessionParams) (PortalSession, error)
}

// ErrNoSubscription is returned by GetSubscriptionForCustomer when a Stripe
// Customer exists but has never completed a checkout. Reconcile (BILL-5)
// needs to tell "no subscription" from "call failed"; a zero value would not.
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
// (design 08 "Checkout Session"). BILL-4 owns computing these; the provider
// owns mapping them onto Stripe (or the stub's local state).
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
}

// PortalSession is the subset of a Stripe Billing Portal Session callers need.
type PortalSession struct {
	URL string
}
