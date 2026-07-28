package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestSubscriptionStoreUpsertRoundTrips covers SubscriptionStore against real
// Postgres: GetByTenant on a missing row is ErrNotFound, Upsert inserts when
// absent, a second Upsert overwrites in full (round-tripping the nullable
// Stripe columns both null and populated), and updated_at advances.
func TestSubscriptionStoreUpsertRoundTrips(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantID := mustNewID(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM subscriptions WHERE tenant_id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
	})
	mustExec(t, db, ctx, `INSERT INTO tenants (id, name) VALUES ($1, 'Subscription Tenant')`, tenantID)

	subs := NewSubscriptionStore(db)

	if _, err := subs.GetByTenant(ctx, tenantID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByTenant before upsert = %v, want ErrNotFound", err)
	}

	// Insert with every Stripe column null (the signup-time shape).
	if err := subs.Upsert(ctx, UpsertSubscriptionParams{
		TenantID: tenantID,
		PlanCode: "starter",
		Comped:   true,
	}); err != nil {
		t.Fatalf("Upsert (insert): %v", err)
	}

	sub, err := subs.GetByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetByTenant after insert: %v", err)
	}
	if sub.PlanCode != "starter" || !sub.Comped {
		t.Fatalf("subscription = %+v, want plan_code=starter comped=true", sub)
	}
	if sub.StripeCustomerID != nil || sub.StripeSubscriptionID != nil || sub.StripeStatus != nil ||
		sub.PastDueSince != nil || sub.CurrentPeriodEnd != nil {
		t.Fatalf("subscription = %+v, want every Stripe column null", sub)
	}
	firstUpdatedAt := sub.UpdatedAt

	// A second Upsert overwrites in full, populating the Stripe columns.
	customerID := "cus_123"
	subscriptionID := "sub_123"
	status := "active"
	if err := subs.Upsert(ctx, UpsertSubscriptionParams{
		TenantID:             tenantID,
		PlanCode:             "starter",
		StripeCustomerID:     &customerID,
		StripeSubscriptionID: &subscriptionID,
		StripeStatus:         &status,
		Comped:               false,
		CancelAtPeriodEnd:    true,
	}); err != nil {
		t.Fatalf("Upsert (update): %v", err)
	}

	updated, err := subs.GetByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetByTenant after update: %v", err)
	}
	if updated.StripeCustomerID == nil || *updated.StripeCustomerID != customerID {
		t.Fatalf("stripe_customer_id = %v, want %q", updated.StripeCustomerID, customerID)
	}
	if updated.StripeSubscriptionID == nil || *updated.StripeSubscriptionID != subscriptionID {
		t.Fatalf("stripe_subscription_id = %v, want %q", updated.StripeSubscriptionID, subscriptionID)
	}
	if updated.StripeStatus == nil || *updated.StripeStatus != status {
		t.Fatalf("stripe_status = %v, want %q", updated.StripeStatus, status)
	}
	if updated.Comped {
		t.Fatal("comped = true, want false after overwrite")
	}
	if !updated.CancelAtPeriodEnd {
		t.Fatal("cancel_at_period_end = false, want true after overwrite")
	}
	if !updated.UpdatedAt.After(firstUpdatedAt) {
		t.Fatalf("updated_at = %v, want later than %v", updated.UpdatedAt, firstUpdatedAt)
	}
}
