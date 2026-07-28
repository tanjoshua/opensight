package billing

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// StubProvider is an in-memory Provider selected by BILLING_PROVIDER=stub
// (design 08 "Local development"). It mirrors llm.StubPromptRunner in
// spirit: no network, deterministic ids, so make up and the whole test
// suite run with zero Stripe calls.
//
// CreateCheckoutSession completes itself immediately and records an active
// subscription (period end 30 days out) — a stub that never completes would
// dead-end the local funnel it exists to exercise, so signup -> checkout ->
// return -> onboarding runs end to end offline with one redirect.
type StubProvider struct {
	mu   sync.Mutex
	next int

	sessions      map[string]CheckoutSession
	subscriptions map[string]Subscription // keyed by Stripe customer id
}

// NewStubProvider returns a ready StubProvider.
func NewStubProvider() *StubProvider {
	return &StubProvider{
		sessions:      make(map[string]CheckoutSession),
		subscriptions: make(map[string]Subscription),
	}
}

// nextID returns a deterministic, monotonically increasing id (cus_stub_1,
// cs_stub_2, sub_stub_3, ...) shared across id kinds, so a test can assert
// on exact ids without the stub reaching out anywhere. Callers must hold mu.
func (p *StubProvider) nextID(prefix string) string {
	p.next++
	return fmt.Sprintf("%s_stub_%d", prefix, p.next)
}

// CreateCustomer returns a new deterministic customer id. The stub does not
// track tenant/email beyond that; nothing downstream of this story reads it.
func (p *StubProvider) CreateCustomer(_ context.Context, _ CreateCustomerParams) (Customer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return Customer{ID: p.nextID("cus")}, nil
}

// CreateCheckoutSession records a completed session and an active
// subscription for params.CustomerID, and substitutes
// {CHECKOUT_SESSION_ID} into the returned URL exactly as Stripe's hosted
// Checkout does.
func (p *StubProvider) CreateCheckoutSession(_ context.Context, params CreateCheckoutSessionParams) (CheckoutSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sessionID := p.nextID("cs")
	subscriptionID := p.nextID("sub")

	session := CheckoutSession{
		ID:                sessionID,
		URL:               strings.ReplaceAll(params.SuccessURL, "{CHECKOUT_SESSION_ID}", sessionID),
		Status:            "complete",
		CustomerID:        params.CustomerID,
		SubscriptionID:    subscriptionID,
		ClientReferenceID: params.TenantID,
	}
	p.sessions[sessionID] = session

	p.subscriptions[params.CustomerID] = Subscription{
		ID:                subscriptionID,
		CustomerID:        params.CustomerID,
		Status:            "active",
		CurrentPeriodEnd:  time.Now().Add(30 * 24 * time.Hour),
		CancelAtPeriodEnd: false,
	}

	return session, nil
}

// GetCheckoutSession returns ErrCheckoutSessionNotFound for an id this stub
// never created, rather than a zero value.
func (p *StubProvider) GetCheckoutSession(_ context.Context, sessionID string) (CheckoutSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	session, ok := p.sessions[sessionID]
	if !ok {
		return CheckoutSession{}, fmt.Errorf("%w: %q", ErrCheckoutSessionNotFound, sessionID)
	}
	return session, nil
}

// GetSubscriptionForCustomer returns ErrNoSubscription for a customer that
// has never completed a checkout, rather than a zero value.
func (p *StubProvider) GetSubscriptionForCustomer(_ context.Context, customerID string) (Subscription, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sub, ok := p.subscriptions[customerID]
	if !ok {
		return Subscription{}, fmt.Errorf("%w: %q", ErrNoSubscription, customerID)
	}
	return sub, nil
}

// CreatePortalSession returns params.ReturnURL as the portal URL, so
// "Manage billing" round-trips locally with no state change.
func (p *StubProvider) CreatePortalSession(_ context.Context, params CreatePortalSessionParams) (PortalSession, error) {
	return PortalSession{URL: params.ReturnURL}, nil
}

// SetSubscriptionStatus drives a customer's stub subscription to an
// arbitrary Stripe status (e.g. past_due, canceled), for tests exercising
// reconcile and access (BILL-5/6/7/10). A no-op if the customer has no
// subscription.
func (p *StubProvider) SetSubscriptionStatus(customerID, status string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sub, ok := p.subscriptions[customerID]
	if !ok {
		return
	}
	sub.Status = status
	p.subscriptions[customerID] = sub
}

// SetCancelAtPeriodEnd drives a customer's stub subscription's
// cancel_at_period_end flag, for tests exercising the cancel-at-period-end
// banner (BILL-10). A no-op if the customer has no subscription.
func (p *StubProvider) SetCancelAtPeriodEnd(customerID string, cancel bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sub, ok := p.subscriptions[customerID]
	if !ok {
		return
	}
	sub.CancelAtPeriodEnd = cancel
	p.subscriptions[customerID] = sub
}
