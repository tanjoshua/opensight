package reconcile

import (
	"context"
	"errors"
	"testing"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/store"

	"github.com/google/uuid"
)

// fakeSubs is a recording fake over subscriptionStore, good enough for apply
// to run against with no database: Upsert overwrites the held row in full,
// same as the real store, so GetByAccount/GetByCustomer on the same fake see
// what a prior Upsert wrote.
type fakeSubs struct {
	sub          store.Subscription
	getErr       error
	getByCustErr error
	upsertErr    error
	upserts      []store.UpsertSubscriptionParams
}

func (f *fakeSubs) GetByAccount(context.Context, domain.ID) (store.Subscription, error) {
	return f.sub, f.getErr
}

func (f *fakeSubs) GetByCustomer(context.Context, string) (store.Subscription, error) {
	return f.sub, f.getByCustErr
}

func (f *fakeSubs) Upsert(_ context.Context, params store.UpsertSubscriptionParams) error {
	f.upserts = append(f.upserts, params)
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.sub = store.Subscription{
		AccountID:             params.AccountID,
		PlanCode:             params.PlanCode,
		Comped:               params.Comped,
		StripeCustomerID:     params.StripeCustomerID,
		StripeSubscriptionID: params.StripeSubscriptionID,
		StripeStatus:         params.StripeStatus,
		PastDueSince:         params.PastDueSince,
		CurrentPeriodEnd:     params.CurrentPeriodEnd,
		CancelAtPeriodEnd:    params.CancelAtPeriodEnd,
	}
	return nil
}

// fakeProvider is a scripted subscriptionProvider: one canned response (or
// error) per call, since these tests drive one customer through a sequence
// of reconciles rather than modelling Stripe's own state.
type fakeProvider struct {
	sub billing.Subscription
	err error
}

func (f *fakeProvider) GetSubscriptionForCustomer(context.Context, string) (billing.Subscription, error) {
	return f.sub, f.err
}

// monitoringCall records one Reconciler -> monitoringGate.Set invocation.
type monitoringCall struct {
	accountID  domain.ID
	platforms []string
	enabled   bool
}

type fakeMonitoringGate struct {
	calls []monitoringCall
	err   error
}

func (f *fakeMonitoringGate) Set(_ context.Context, accountID domain.ID, platforms []string, enabled bool) error {
	f.calls = append(f.calls, monitoringCall{accountID, platforms, enabled})
	return f.err
}

func testAccountID(t *testing.T) domain.ID {
	t.Helper()
	return uuid.New()
}

// TestDunningAnchorSetOnceAndClearsOnRecovery covers the AC that dunning is
// anchored once when it begins and cleared when it ends, and that repeated
// updates during the same dunning cycle do not push the anchor forward (or
// BILL-6's 21-day bound would never expire).
func TestDunningAnchorSetOnceAndClearsOnRecovery(t *testing.T) {
	accountID := testAccountID(t)
	customerID := "cus_1"
	subs := &fakeSubs{sub: store.Subscription{AccountID: accountID, PlanCode: billing.Starter.Code, StripeCustomerID: &customerID}}
	provider := &fakeProvider{sub: billing.Subscription{ID: "sub_1", Status: "past_due"}}

	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := New(subs, provider, nil, nil, func() time.Time { return clock })

	// First reconcile while past_due: anchor set to "now".
	sub, err := r.Account(context.Background(), accountID)
	if err != nil {
		t.Fatalf("Account (first past_due): %v", err)
	}
	if sub.PastDueSince == nil || !sub.PastDueSince.Equal(clock) {
		t.Fatalf("PastDueSince = %v, want %v", sub.PastDueSince, clock)
	}
	firstAnchor := *sub.PastDueSince

	// Clock advances, still past_due: anchor must not move.
	clock = clock.Add(5 * 24 * time.Hour)
	sub, err = r.Account(context.Background(), accountID)
	if err != nil {
		t.Fatalf("Account (second past_due): %v", err)
	}
	if sub.PastDueSince == nil || !sub.PastDueSince.Equal(firstAnchor) {
		t.Fatalf("PastDueSince after repeated past_due = %v, want unchanged %v", sub.PastDueSince, firstAnchor)
	}

	// Recovery: status leaves past_due, anchor clears.
	provider.sub = billing.Subscription{ID: "sub_1", Status: "active"}
	sub, err = r.Account(context.Background(), accountID)
	if err != nil {
		t.Fatalf("Account (recovery): %v", err)
	}
	if sub.PastDueSince != nil {
		t.Fatalf("PastDueSince after recovery = %v, want nil", sub.PastDueSince)
	}
}

// TestMonitoringPausesOnFullToLapsed covers the AC that a state change also
// stops monitoring: active -> canceled pauses, using the account's plan
// platforms, never a literal.
func TestMonitoringPausesOnFullToLapsed(t *testing.T) {
	accountID := testAccountID(t)
	customerID := "cus_1"
	subs := &fakeSubs{sub: store.Subscription{
		AccountID: accountID, PlanCode: billing.Starter.Code, StripeCustomerID: &customerID,
		StripeSubscriptionID: strPtr("sub_1"), StripeStatus: strPtr("active"),
	}}
	provider := &fakeProvider{sub: billing.Subscription{ID: "sub_1", Status: "canceled"}}
	monitoring := &fakeMonitoringGate{}
	r := New(subs, provider, monitoring, nil, func() time.Time { return time.Now() })

	if _, err := r.Account(context.Background(), accountID); err != nil {
		t.Fatalf("Account: %v", err)
	}

	if len(monitoring.calls) != 1 {
		t.Fatalf("monitoring calls = %d, want 1", len(monitoring.calls))
	}
	call := monitoring.calls[0]
	if call.enabled {
		t.Fatal("enabled = true, want false (pausing)")
	}
	if call.accountID != accountID {
		t.Fatalf("accountID = %v, want %v", call.accountID, accountID)
	}
	if len(call.platforms) != len(billing.Starter.Platforms) || call.platforms[0] != billing.Starter.Platforms[0] {
		t.Fatalf("platforms = %v, want %v (from the plan catalog)", call.platforms, billing.Starter.Platforms)
	}
}

// TestMonitoringResumesOnLapsedToFull covers the mirror AC: canceled ->
// active resumes.
func TestMonitoringResumesOnLapsedToFull(t *testing.T) {
	accountID := testAccountID(t)
	customerID := "cus_1"
	subs := &fakeSubs{sub: store.Subscription{
		AccountID: accountID, PlanCode: billing.Starter.Code, StripeCustomerID: &customerID,
		StripeSubscriptionID: strPtr("sub_1"), StripeStatus: strPtr("canceled"),
	}}
	provider := &fakeProvider{sub: billing.Subscription{ID: "sub_1", Status: "active"}}
	monitoring := &fakeMonitoringGate{}
	r := New(subs, provider, monitoring, nil, func() time.Time { return time.Now() })

	if _, err := r.Account(context.Background(), accountID); err != nil {
		t.Fatalf("Account: %v", err)
	}

	if len(monitoring.calls) != 1 {
		t.Fatalf("monitoring calls = %d, want 1", len(monitoring.calls))
	}
	if !monitoring.calls[0].enabled {
		t.Fatal("enabled = false, want true (resuming)")
	}
}

// TestMonitoringDesiredStateReasserted covers retry repair: active -> active
// still asserts that monitoring is enabled.
func TestMonitoringDesiredStateReasserted(t *testing.T) {
	accountID := testAccountID(t)
	customerID := "cus_1"
	subs := &fakeSubs{sub: store.Subscription{
		AccountID: accountID, PlanCode: billing.Starter.Code, StripeCustomerID: &customerID,
		StripeSubscriptionID: strPtr("sub_1"), StripeStatus: strPtr("active"),
	}}
	provider := &fakeProvider{sub: billing.Subscription{ID: "sub_1", Status: "active"}}
	monitoring := &fakeMonitoringGate{}
	r := New(subs, provider, monitoring, nil, func() time.Time { return time.Now() })

	if _, err := r.Account(context.Background(), accountID); err != nil {
		t.Fatalf("Account: %v", err)
	}

	if len(monitoring.calls) != 1 {
		t.Fatalf("monitoring calls = %d, want 1", len(monitoring.calls))
	}
	if !monitoring.calls[0].enabled {
		t.Fatal("enabled = false, want true")
	}
}

func TestMonitoringFailureIsRetryable(t *testing.T) {
	accountID := testAccountID(t)
	customerID := "cus_1"
	subs := &fakeSubs{sub: store.Subscription{
		AccountID: accountID, PlanCode: billing.Starter.Code, StripeCustomerID: &customerID,
		StripeSubscriptionID: strPtr("sub_1"), StripeStatus: strPtr("active"),
	}}
	provider := &fakeProvider{sub: billing.Subscription{ID: "sub_1", Status: "active"}}
	monitoring := &fakeMonitoringGate{err: errBoom}
	r := New(subs, provider, monitoring, nil, nil)

	if _, err := r.Account(context.Background(), accountID); !errors.Is(err, errBoom) {
		t.Fatalf("Account monitoring failure = %v, want wrapped errBoom", err)
	}
}

// TestNilMonitoringIsNoOp covers the AC that reconcile tolerates a nil
// monitoring seam: an access-crossing reconcile still writes the row and
// returns no error with monitoring nil (BILL-4's checkout-funnel tests build
// Reconcilers with no Temporal client in scope).
func TestNilMonitoringIsNoOp(t *testing.T) {
	accountID := testAccountID(t)
	customerID := "cus_1"
	subs := &fakeSubs{sub: store.Subscription{
		AccountID: accountID, PlanCode: billing.Starter.Code, StripeCustomerID: &customerID,
		StripeSubscriptionID: strPtr("sub_1"), StripeStatus: strPtr("active"),
	}}
	provider := &fakeProvider{sub: billing.Subscription{ID: "sub_1", Status: "canceled"}}
	r := New(subs, provider, nil, nil, func() time.Time { return time.Now() })

	sub, err := r.Account(context.Background(), accountID)
	if err != nil {
		t.Fatalf("Account with nil monitoring: %v", err)
	}
	if sub.StripeStatus == nil || *sub.StripeStatus != "canceled" {
		t.Fatalf("stripe_status = %v, want canceled (the row write must not depend on monitoring)", sub.StripeStatus)
	}
}

// TestByCustomerWrapsNotFound covers the webhook handler's needed
// distinction: an unknown customer id surfaces as a wrapped store.ErrNotFound
// so the handler can tell an orphan Customer apart from a real failure.
func TestByCustomerWrapsNotFound(t *testing.T) {
	subs := &fakeSubs{getByCustErr: store.ErrNotFound}
	r := New(subs, &fakeProvider{}, nil, nil, nil)

	_, err := r.ByCustomer(context.Background(), "cus_unknown")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ByCustomer(unknown) = %v, want wrapped ErrNotFound", err)
	}
}

type fakeCustomerLocker struct {
	keys  []string
	calls int
}

func (f *fakeCustomerLocker) WithLock(ctx context.Context, key string, fn func(context.Context) error) error {
	f.keys = append(f.keys, key)
	f.calls++
	return fn(ctx)
}

func TestByCustomerUsesCustomerScopedLock(t *testing.T) {
	accountID := testAccountID(t)
	customerID := "cus_1"
	subs := &fakeSubs{sub: store.Subscription{
		AccountID: accountID, PlanCode: billing.Starter.Code, StripeCustomerID: &customerID,
	}}
	locks := &fakeCustomerLocker{}
	r := New(subs, &fakeProvider{err: billing.ErrNoSubscription}, nil, locks, nil)

	if _, err := r.ByCustomer(context.Background(), customerID); err != nil {
		t.Fatalf("ByCustomer: %v", err)
	}
	if locks.calls != 1 || len(locks.keys) != 1 || locks.keys[0] != "billing-customer:"+customerID {
		t.Fatalf("locks = calls:%d keys:%v, want one customer-scoped lock", locks.calls, locks.keys)
	}
}

func strPtr(s string) *string { return &s }
