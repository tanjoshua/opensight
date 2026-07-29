// Package reconcile is the single path that writes billing state (design 08
// "Reconcile"). The webhook (BILL-5) and the checkout return (BILL-4) both
// funnel into the unexported apply, so the two entry points cannot disagree.
//
// This is a package of its own, not a method on api.Server and not a file in
// internal/billing. internal/billing cannot host it: its seam would need to
// speak store.Subscription, and internal/store already imports internal/billing
// (the plan catalog) — a direct cycle. Hanging it off api.Server would
// compile, but reconcile would then need a many-field Server to test, and a
// future `opensight tenant comp` CLI command would have to reach into
// internal/api just to change billing state. A subpackage created
// specifically to keep a dependency out of internal/billing is exactly what
// internal/billing/stripe already is.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/store"
)

// subscriptionStore is the seam reconcile needs: load a tenant's row and
// write it back in full. Narrower than *store.SubscriptionStore so a test can
// fake it with no database.
type subscriptionStore interface {
	GetByTenant(ctx context.Context, tenantID domain.ID) (store.Subscription, error)
	Upsert(ctx context.Context, params store.UpsertSubscriptionParams) error
}

// subscriptionProvider is the one billing.Provider method reconcile needs.
// CreateCustomer/CreateCheckoutSession/GetCheckoutSession belong to the RPC
// layer's checkout flow (internal/api's billingProvider seam), not here.
type subscriptionProvider interface {
	GetSubscriptionForCustomer(ctx context.Context, customerID string) (billing.Subscription, error)
}

// ErrNoCustomer means the tenant never started a checkout — a real state,
// distinguishable from a failure. apply returns it instead of writing
// anything, since there is nothing for the caller to look up yet.
var ErrNoCustomer = errors.New("tenant has no stripe customer")

// Reconciler holds reconcile's dependencies.
//
// BILL-5 extends this in two ways this file leaves room for: a nil-tolerant
// schedule seam (pause/unpause the tenant's monitoring Schedule when access
// changes) added as a field here, and a second entry point, ByCustomer, that
// loads the row by stripe_customer_id (the webhook only carries a customer
// id, never a tenant id) and calls the same apply. Both land alongside an
// access-changed comparison wrapped around step 3 below.
type Reconciler struct {
	subscriptions subscriptionStore
	provider      subscriptionProvider
	now           func() time.Time
}

// New builds a Reconciler. now defaults to time.Now when nil, so production
// call sites don't have to pass it and tests can substitute a fixed clock.
func New(subscriptions subscriptionStore, provider subscriptionProvider, now func() time.Time) *Reconciler {
	if now == nil {
		now = time.Now
	}
	return &Reconciler{subscriptions: subscriptions, provider: provider, now: now}
}

// Tenant loads tenantID's subscription row and reconciles it against Stripe.
// This is BILL-4's entry point (ConfirmCheckout reconciles by tenant, since
// the session carries the tenant id). BILL-5's ByCustomer is the webhook's.
func (r *Reconciler) Tenant(ctx context.Context, tenantID domain.ID) (store.Subscription, error) {
	sub, err := r.subscriptions.GetByTenant(ctx, tenantID)
	if err != nil {
		return store.Subscription{}, fmt.Errorf("reconcile: load subscription: %w", err)
	}
	return r.apply(ctx, sub)
}

// apply is the single writer (design 08). Precisely:
//  1. No Customer yet → ErrNoCustomer, nothing written.
//  2. Customer exists but has never completed a checkout (ErrNoSubscription)
//     → write nothing, return sub unchanged. Any other provider error
//     propagates wrapped, so a Stripe retry (BILL-5) means something.
//  3. Otherwise upsert the full row: identity fields (TenantID, PlanCode,
//     Comped, StripeCustomerID) carried verbatim from the loaded row, never
//     invented here — that is what makes the full-row overwrite safe. Stripe
//     fields (StripeSubscriptionID, StripeStatus, CancelAtPeriodEnd,
//     CurrentPeriodEnd) come from the freshly fetched remote state.
func (r *Reconciler) apply(ctx context.Context, sub store.Subscription) (store.Subscription, error) {
	if sub.StripeCustomerID == nil {
		return sub, ErrNoCustomer
	}

	remote, err := r.provider.GetSubscriptionForCustomer(ctx, *sub.StripeCustomerID)
	if err != nil {
		if errors.Is(err, billing.ErrNoSubscription) {
			return sub, nil
		}
		return store.Subscription{}, fmt.Errorf("reconcile: get subscription for customer: %w", err)
	}

	status := remote.Status
	subscriptionID := remote.ID
	var currentPeriodEnd *time.Time
	if !remote.CurrentPeriodEnd.IsZero() {
		cpe := remote.CurrentPeriodEnd
		currentPeriodEnd = &cpe
	}

	params := store.UpsertSubscriptionParams{
		TenantID:             sub.TenantID,
		PlanCode:             sub.PlanCode,
		Comped:               sub.Comped,
		StripeCustomerID:     sub.StripeCustomerID,
		StripeSubscriptionID: &subscriptionID,
		StripeStatus:         &status,
		PastDueSince:         dunningAnchor(sub.PastDueSince, remote.Status, r.now()),
		CurrentPeriodEnd:     currentPeriodEnd,
		CancelAtPeriodEnd:    remote.CancelAtPeriodEnd,
	}
	if err := r.subscriptions.Upsert(ctx, params); err != nil {
		return store.Subscription{}, fmt.Errorf("reconcile: upsert subscription: %w", err)
	}

	// Constructed locally rather than re-read: one query, and the caller
	// needs exactly what was just written.
	return store.Subscription{
		TenantID:             params.TenantID,
		PlanCode:             params.PlanCode,
		Comped:               params.Comped,
		StripeCustomerID:     params.StripeCustomerID,
		StripeSubscriptionID: params.StripeSubscriptionID,
		StripeStatus:         params.StripeStatus,
		PastDueSince:         params.PastDueSince,
		CurrentPeriodEnd:     params.CurrentPeriodEnd,
		CancelAtPeriodEnd:    params.CancelAtPeriodEnd,
	}, nil
}

// dunningAnchor computes past_due_since for a write. Its AC and test belong
// to BILL-5 (repeated updates during one dunning cycle must not push the
// anchor forward), but apply needs it correct now: without it, apply either
// writes NULL over a live anchor on every reconcile, or (if it instead always
// carried the previous value forward) never clears it once dunning ends.
func dunningAnchor(previous *time.Time, remoteStatus string, now time.Time) *time.Time {
	if remoteStatus != "past_due" {
		return nil
	}
	if previous != nil {
		return previous
	}
	anchor := now
	return &anchor
}
