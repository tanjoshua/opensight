package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// Subscription is a persisted subscriptions row: a tenant's permanent billing
// record (migration 00010, design 08 Schema). One row per tenant, created at
// signup with every Stripe column null.
type Subscription struct {
	TenantID             domain.ID
	PlanCode             string
	StripeCustomerID     *string
	StripeSubscriptionID *string
	// StripeStatus is verbatim Stripe status text; never a locally-invented
	// value (design 08).
	StripeStatus      *string
	PastDueSince      *time.Time
	Comped            bool
	CurrentPeriodEnd  *time.Time
	CancelAtPeriodEnd bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// AccessState projects a Subscription onto the primitives billing.State takes
// (internal/store imports internal/billing for the plan catalog, so the
// derivation cannot take a Subscription directly without an import cycle;
// this is the one adapter). Delegates to billingStateFromRow, shared with
// GetSession's joined-column path so the two dereferences can't
// drift.
func (s Subscription) AccessState() billing.State {
	return billingStateFromRow(s.Comped, s.StripeSubscriptionID, s.StripeStatus, s.PastDueSince)
}

// billingStateFromRow builds a billing.State from the nullable Stripe
// columns shared by a subscriptions row (Subscription.AccessState) and
// GetSession's LEFT JOINed columns — the one place
// both paths dereference, so they can't drift. A nil
// *string/*time.Time dereferences to its zero value, which billing.DeriveAccess
// treats correctly (empty StripeStatus falls through to the never/lapsed
// arms; a zero PastDueSince is handled explicitly).
func billingStateFromRow(comped bool, stripeSubscriptionID, stripeStatus *string, pastDueSince *time.Time) billing.State {
	st := billing.State{Comped: comped}
	if stripeSubscriptionID != nil {
		st.StripeSubscriptionID = *stripeSubscriptionID
	}
	if stripeStatus != nil {
		st.StripeStatus = *stripeStatus
	}
	if pastDueSince != nil {
		st.PastDueSince = *pastDueSince
	}
	return st
}

// UpsertSubscriptionParams are every subscriptions column. The param struct is
// deliberately explicit (not a partial/patch shape) so a partial write can
// never silently null a Stripe column out from under a concurrent reconcile.
type UpsertSubscriptionParams struct {
	TenantID             domain.ID
	PlanCode             string
	StripeCustomerID     *string
	StripeSubscriptionID *string
	StripeStatus         *string
	PastDueSince         *time.Time
	Comped               bool
	CurrentPeriodEnd     *time.Time
	CancelAtPeriodEnd    bool
}

// GetByTenant loads a tenant's subscription row. A missing row (no tenant, or
// a tenant somehow created without one) returns ErrNotFound.
func (s *Store) GetByTenant(ctx context.Context, tenantID domain.ID) (Subscription, error) {

	row, err := s.q(ctx).GetSubscriptionByTenant(ctx, tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscription{}, ErrNotFound
		}
		return Subscription{}, fmt.Errorf("get subscription: %w", err)
	}
	return subscriptionFromSQLC(row), nil
}

// GetByCustomer loads the subscription row for a Stripe Customer id — the
// webhook's lookup: a delivery only ever carries a Customer id,
// never a tenant id. stripe_customer_id is UNIQUE, so at most one row can
// match; no match returns ErrNotFound.
func (s *Store) GetByCustomer(ctx context.Context, customerID string) (Subscription, error) {

	row, err := s.q(ctx).GetSubscriptionByCustomer(ctx, &customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscription{}, ErrNotFound
		}
		return Subscription{}, fmt.Errorf("get subscription by customer: %w", err)
	}
	return subscriptionFromSQLC(row), nil
}

// Upsert writes every subscription column, inserting the row if absent and
// otherwise overwriting it in full (bumping updated_at). This is the seam a
// future reconcile (design 08) writes through.
func (s *Store) Upsert(ctx context.Context, params UpsertSubscriptionParams) error {

	err := s.q(ctx).UpsertSubscription(ctx, storesqlc.UpsertSubscriptionParams{
		TenantID: params.TenantID, PlanCode: params.PlanCode, StripeCustomerID: params.StripeCustomerID,
		StripeSubscriptionID: params.StripeSubscriptionID, StripeStatus: params.StripeStatus,
		PastDueSince: params.PastDueSince, Comped: params.Comped, CurrentPeriodEnd: params.CurrentPeriodEnd,
		CancelAtPeriodEnd: params.CancelAtPeriodEnd,
	})
	if err != nil {
		return fmt.Errorf("upsert subscription: %w", err)
	}
	return nil
}

// SetStripeCustomerID persists a tenant's Stripe Customer id write-once and
// returns the id that won. A tenant that already has a Customer keeps it —
// the id is permanent (design 08 "Customer"), enforced by this statement
// rather than by every caller remembering to check first. Touches no other
// column, so it cannot race a concurrent reconcile write.
//
// Zero rows (unknown tenant) returns ErrNotFound. The UNIQUE constraint on
// stripe_customer_id can only be violated across tenants, which needs Stripe
// to have reissued an id already assigned to another tenant — that is our
// bug, not the caller's, and is left to the generic rpcError→CodeInternal
// mapping rather than a dedicated sentinel.
func (s *Store) SetStripeCustomerID(ctx context.Context, tenantID domain.ID, customerID string) (string, error) {

	won, err := s.q(ctx).SetStripeCustomerID(ctx, storesqlc.SetStripeCustomerIDParams{
		TenantID: tenantID, StripeCustomerID: &customerID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("set stripe customer id: %w", err)
	}
	return *won, nil
}

// CreateSubscriptionInTx inserts a starter subscription row for a
// just-created tenant, in the same transaction as the tenant insert
// (CreateTenant and CreateAccount). Every Stripe
// column stays null.
func CreateSubscriptionInTx(ctx context.Context, q *storesqlc.Queries, tenantID domain.ID, planCode string, comped bool) error {
	if err := q.InsertSubscription(ctx, storesqlc.InsertSubscriptionParams{TenantID: tenantID, PlanCode: planCode, Comped: comped}); err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}
	return nil
}

func subscriptionFromSQLC(row storesqlc.Subscription) Subscription {
	return Subscription{
		TenantID: row.TenantID, PlanCode: row.PlanCode, StripeCustomerID: row.StripeCustomerID,
		StripeSubscriptionID: row.StripeSubscriptionID, StripeStatus: row.StripeStatus,
		PastDueSince: row.PastDueSince, Comped: row.Comped, CurrentPeriodEnd: row.CurrentPeriodEnd,
		CancelAtPeriodEnd: row.CancelAtPeriodEnd, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
