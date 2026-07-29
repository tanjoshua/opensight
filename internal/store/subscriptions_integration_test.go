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

// TestSubscriptionStoreSetStripeCustomerID covers the write-once behaviour
// the UPDATE ... COALESCE ... RETURNING statement exists for: only real
// Postgres proves a second write with a different id loses, and that the
// statement touches no column besides stripe_customer_id (and updated_at on
// the winning write) — the concurrency guarantee Upsert cannot give (design
// 08 "Customer").
func TestSubscriptionStoreSetStripeCustomerID(t *testing.T) {
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
	mustExec(t, db, ctx, `INSERT INTO tenants (id, name) VALUES ($1, 'Customer Id Tenant')`, tenantID)

	subs := NewSubscriptionStore(db)

	if _, err := subs.SetStripeCustomerID(ctx, mustNewID(t), "cus_ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetStripeCustomerID(unknown tenant) = %v, want ErrNotFound", err)
	}

	// A prior full Upsert with populated Stripe columns must survive
	// untouched: SetStripeCustomerID only ever writes stripe_customer_id (and
	// updated_at on the winning write).
	subscriptionID := "sub_before"
	status := "active"
	if err := subs.Upsert(ctx, UpsertSubscriptionParams{
		TenantID:             tenantID,
		PlanCode:             "starter",
		StripeSubscriptionID: &subscriptionID,
		StripeStatus:         &status,
	}); err != nil {
		t.Fatalf("Upsert (seed): %v", err)
	}

	won, err := subs.SetStripeCustomerID(ctx, tenantID, "cus_first")
	if err != nil {
		t.Fatalf("SetStripeCustomerID (first): %v", err)
	}
	if won != "cus_first" {
		t.Fatalf("SetStripeCustomerID (first) = %q, want cus_first", won)
	}

	after, err := subs.GetByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetByTenant after first SetStripeCustomerID: %v", err)
	}
	if after.StripeCustomerID == nil || *after.StripeCustomerID != "cus_first" {
		t.Fatalf("stripe_customer_id = %v, want cus_first", after.StripeCustomerID)
	}
	if after.StripeSubscriptionID == nil || *after.StripeSubscriptionID != subscriptionID {
		t.Fatalf("stripe_subscription_id = %v, want it untouched (%q)", after.StripeSubscriptionID, subscriptionID)
	}
	if after.StripeStatus == nil || *after.StripeStatus != status {
		t.Fatalf("stripe_status = %v, want it untouched (%q)", after.StripeStatus, status)
	}

	// A second call with a different id loses: the first id already won.
	won, err = subs.SetStripeCustomerID(ctx, tenantID, "cus_second")
	if err != nil {
		t.Fatalf("SetStripeCustomerID (second): %v", err)
	}
	if won != "cus_first" {
		t.Fatalf("SetStripeCustomerID (second) = %q, want cus_first (write-once)", won)
	}

	final, err := subs.GetByTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("GetByTenant after second SetStripeCustomerID: %v", err)
	}
	if final.StripeCustomerID == nil || *final.StripeCustomerID != "cus_first" {
		t.Fatalf("stripe_customer_id after second call = %v, want cus_first unchanged", final.StripeCustomerID)
	}
}
