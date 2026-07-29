package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
)

// Subscription is a persisted subscriptions row: a tenant's permanent billing
// record (migration 00010, design 08 Schema). One row per tenant, created at
// signup (or, pre-BILL-3, by CreateTenantInTx) with every Stripe column null.
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
// this is the one adapter). A nil *string/*time.Time dereferences to its zero
// value, which billing.DeriveAccess treats correctly (empty StripeStatus
// falls through to the never/lapsed arms; a zero PastDueSince is handled
// explicitly).
func (s Subscription) AccessState() billing.State {
	st := billing.State{Comped: s.Comped}
	if s.StripeSubscriptionID != nil {
		st.StripeSubscriptionID = *s.StripeSubscriptionID
	}
	if s.StripeStatus != nil {
		st.StripeStatus = *s.StripeStatus
	}
	if s.PastDueSince != nil {
		st.PastDueSince = *s.PastDueSince
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

// SubscriptionStore reads and writes subscriptions rows.
type SubscriptionStore struct {
	db *sql.DB
}

// NewSubscriptionStore returns a SubscriptionStore backed by db.
func NewSubscriptionStore(db *sql.DB) *SubscriptionStore {
	return &SubscriptionStore{db: db}
}

const (
	subscriptionColumns = `tenant_id, plan_code, stripe_customer_id, stripe_subscription_id,
       stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end,
       created_at, updated_at`

	getSubscriptionByTenantSQL = `
SELECT ` + subscriptionColumns + `
FROM subscriptions
WHERE tenant_id = $1`

	upsertSubscriptionSQL = `
INSERT INTO subscriptions (
  tenant_id, plan_code, stripe_customer_id, stripe_subscription_id,
  stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9
)
ON CONFLICT (tenant_id) DO UPDATE SET
  plan_code = EXCLUDED.plan_code,
  stripe_customer_id = EXCLUDED.stripe_customer_id,
  stripe_subscription_id = EXCLUDED.stripe_subscription_id,
  stripe_status = EXCLUDED.stripe_status,
  past_due_since = EXCLUDED.past_due_since,
  comped = EXCLUDED.comped,
  current_period_end = EXCLUDED.current_period_end,
  cancel_at_period_end = EXCLUDED.cancel_at_period_end,
  updated_at = now()`

	// insertSubscriptionSQL is the plain insert CreateSubscriptionInTx uses when
	// creating a tenant: there is no existing row to conflict with, so an insert
	// keeps the intent explicit rather than reusing the upsert's ON CONFLICT arm.
	insertSubscriptionSQL = `
INSERT INTO subscriptions (tenant_id, plan_code, comped)
VALUES ($1, $2, $3)`

	// setStripeCustomerIDSQL writes stripe_customer_id write-once and touches
	// no other column, so it cannot race a concurrent reconcile write (unlike
	// Upsert, a full-row overwrite built from a row read before a network
	// round trip).
	setStripeCustomerIDSQL = `
UPDATE subscriptions
SET stripe_customer_id = COALESCE(stripe_customer_id, $2),
    updated_at         = CASE WHEN stripe_customer_id IS NULL THEN now() ELSE updated_at END
WHERE tenant_id = $1
RETURNING stripe_customer_id`
)

// GetByTenant loads a tenant's subscription row. A missing row (no tenant, or
// a tenant somehow created without one) returns ErrNotFound.
func (s *SubscriptionStore) GetByTenant(ctx context.Context, tenantID domain.ID) (Subscription, error) {
	if s == nil || s.db == nil {
		return Subscription{}, errors.New("subscription store database is required")
	}

	sub, err := scanSubscription(s.db.QueryRowContext(ctx, getSubscriptionByTenantSQL, tenantID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Subscription{}, ErrNotFound
		}
		return Subscription{}, fmt.Errorf("get subscription: %w", err)
	}
	return sub, nil
}

// Upsert writes every subscription column, inserting the row if absent and
// otherwise overwriting it in full (bumping updated_at). This is the seam a
// future reconcile (design 08) writes through.
func (s *SubscriptionStore) Upsert(ctx context.Context, params UpsertSubscriptionParams) error {
	if s == nil || s.db == nil {
		return errors.New("subscription store database is required")
	}

	_, err := s.db.ExecContext(ctx, upsertSubscriptionSQL,
		params.TenantID, params.PlanCode, params.StripeCustomerID, params.StripeSubscriptionID,
		params.StripeStatus, params.PastDueSince, params.Comped, params.CurrentPeriodEnd,
		params.CancelAtPeriodEnd,
	)
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
func (s *SubscriptionStore) SetStripeCustomerID(ctx context.Context, tenantID domain.ID, customerID string) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("subscription store database is required")
	}

	var won string
	err := s.db.QueryRowContext(ctx, setStripeCustomerIDSQL, tenantID, customerID).Scan(&won)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("set stripe customer id: %w", err)
	}
	return won, nil
}

// CreateSubscriptionInTx inserts a starter subscription row for a
// just-created tenant, in the same transaction as the tenant insert
// (AccountStore.CreateTenant and AccountStore.CreateAccount). Every Stripe
// column stays null.
func CreateSubscriptionInTx(ctx context.Context, q querier, tenantID domain.ID, planCode string, comped bool) error {
	if _, err := q.execContext(ctx, insertSubscriptionSQL, tenantID, planCode, comped); err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}
	return nil
}

func scanSubscription(row rowScanner) (Subscription, error) {
	var sub Subscription
	if err := row.Scan(
		&sub.TenantID, &sub.PlanCode, &sub.StripeCustomerID, &sub.StripeSubscriptionID,
		&sub.StripeStatus, &sub.PastDueSince, &sub.Comped, &sub.CurrentPeriodEnd,
		&sub.CancelAtPeriodEnd, &sub.CreatedAt, &sub.UpdatedAt,
	); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}
