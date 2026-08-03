package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"opensight/internal/billing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAccountStoreCreateTenantAndUser(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	admin := New(db)
	tenant, err := admin.CreateTenant(ctx, CreateTenantParams{Name: "  Admin Tenant  "})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE tenant_id = $1", tenant.ID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE tenant_id = $1", tenant.ID)
		_, _ = db.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	if tenant.Name != "Admin Tenant" {
		t.Fatalf("tenant name = %q, want trimmed name", tenant.Name)
	}

	// CreateTenant inserts the subscription row in the same transaction: a
	// CLI-provisioned tenant is comped on the starter plan (design 08 "Operator
	// comps").
	var planCode string
	var comped bool
	if err := db.QueryRow(ctx, "SELECT plan_code, comped FROM subscriptions WHERE tenant_id = $1", tenant.ID).Scan(&planCode, &comped); err != nil {
		t.Fatalf("load tenant subscription: %v", err)
	}
	if planCode != billing.Starter.Code {
		t.Fatalf("plan code = %q, want %q", planCode, billing.Starter.Code)
	}
	if !comped {
		t.Fatal("comped = false, want true for a CLI-provisioned tenant")
	}

	// CreateUser leaves google_sub unset: the operator provisions the row, and
	// the user's first Google sign-in links it (design 07 "Auth and
	// accounts").
	user, err := admin.CreateUser(ctx, CreateUserParams{
		TenantID: tenant.ID,
		Email:    "  Owner@Example.com  ",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if user.TenantID != tenant.ID {
		t.Fatalf("user tenant id = %s, want %s", user.TenantID, tenant.ID)
	}
	if user.Email != "owner@example.com" {
		t.Fatalf("user email = %q, want lowercase trimmed email", user.Email)
	}

	var googleSub *string
	if err := db.QueryRow(ctx, "SELECT google_sub FROM users WHERE id = $1", user.ID).Scan(&googleSub); err != nil {
		t.Fatalf("load user google_sub: %v", err)
	}
	if googleSub != nil {
		t.Fatalf("google_sub = %v, want NULL until the first Google sign-in", *googleSub)
	}

	// SubscriptionStore.GetByTenant plus the catalog resolves the entitlements
	// the RUN-5 schedule derives its interval from.
	subscriptions := New(db)
	sub, err := subscriptions.GetByTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("GetByTenant: %v", err)
	}
	plan, err := billing.PlanFor(sub.PlanCode)
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	if plan.Code != billing.Starter.Code {
		t.Fatalf("plan code = %q, want %q", plan.Code, billing.Starter.Code)
	}
	if plan.RunInterval != "weekly" {
		t.Fatalf("run interval = %q, want weekly", plan.RunInterval)
	}
	if plan.PromptLimit != 20 {
		t.Fatalf("prompt limit = %d, want 20", plan.PromptLimit)
	}
}

// TestAccountStoreCreateAccount is the BILL-3 atomicity + shape acceptance
// test: CreateAccount yields a tenant named from the email local part, an
// uncomped starter subscription, a user, and no business row.
func TestAccountStoreCreateAccount(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	accounts := New(db)

	tenant, user, err := accounts.CreateAccount(ctx, CreateAccountParams{
		Email:     "  Founder@Example.com  ",
		GoogleSub: "google-sub-founder",
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE tenant_id = $1", tenant.ID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE tenant_id = $1", tenant.ID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE tenant_id = $1", tenant.ID)
		_, _ = db.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	if tenant.Name != "founder" {
		t.Fatalf("tenant name = %q, want email local part %q", tenant.Name, "founder")
	}
	if user.Email != "founder@example.com" {
		t.Fatalf("user email = %q, want lowercase trimmed email", user.Email)
	}
	if user.TenantID != tenant.ID {
		t.Fatalf("user tenant id = %s, want %s", user.TenantID, tenant.ID)
	}

	var planCode string
	var comped bool
	var stripeCustomerID, stripeSubscriptionID, stripeStatus *string
	if err := db.QueryRow(ctx, `
		SELECT plan_code, comped, stripe_customer_id, stripe_subscription_id, stripe_status FROM subscriptions WHERE tenant_id = $1`, tenant.ID).Scan(&planCode, &comped, &stripeCustomerID, &stripeSubscriptionID, &stripeStatus); err != nil {
		t.Fatalf("load tenant subscription: %v", err)
	}
	if planCode != billing.Starter.Code {
		t.Fatalf("plan code = %q, want %q", planCode, billing.Starter.Code)
	}
	if comped {
		t.Fatal("comped = true, want false for a self-serve signup")
	}
	if stripeCustomerID != nil || stripeSubscriptionID != nil || stripeStatus != nil {
		t.Fatalf("stripe columns not all null: customer=%v subscription=%v status=%v", stripeCustomerID, stripeSubscriptionID, stripeStatus)
	}

	businesses := New(db)
	list, err := businesses.ListBusinesses(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("ListBusinesses: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("businesses = %d, want 0 (account survives with no business)", len(list))
	}
}

// TestAccountStoreCreateAccountDuplicateEmailRollsBack is the BILL-3
// atomicity acceptance test: a duplicate-email signup fails with
// ErrEmailTaken and leaves no orphan tenant or subscription behind.
func TestAccountStoreCreateAccountDuplicateEmailRollsBack(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	accounts := New(db)

	tenant, _, err := accounts.CreateAccount(ctx, CreateAccountParams{
		Email:     "duplicate@example.com",
		GoogleSub: "google-sub-duplicate-1",
	})
	if err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE tenant_id = $1", tenant.ID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE tenant_id = $1", tenant.ID)
		_, _ = db.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	var tenantsBefore int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM tenants").Scan(&tenantsBefore); err != nil {
		t.Fatalf("count tenants before: %v", err)
	}

	_, _, err = accounts.CreateAccount(ctx, CreateAccountParams{
		Email:     "Duplicate@Example.com",
		GoogleSub: "google-sub-duplicate-2",
	})
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("second CreateAccount error = %v, want ErrEmailTaken", err)
	}

	var tenantsAfter int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM tenants").Scan(&tenantsAfter); err != nil {
		t.Fatalf("count tenants after: %v", err)
	}
	if tenantsAfter != tenantsBefore {
		t.Fatalf("tenant count changed from %d to %d; duplicate signup left an orphan tenant", tenantsBefore, tenantsAfter)
	}
}
