package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"opensight/internal/billing"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
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

	admin := NewAccountStore(db)
	tenant, err := admin.CreateTenant(ctx, CreateTenantParams{Name: "  Admin Tenant  "})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query051, tenant.ID)
		_, _ = testdb.Exec(ctx, db, testdb.Query052, tenant.ID)
		_, _ = testdb.Exec(ctx, db, testdb.Query053, tenant.ID)
	})

	if tenant.Name != "Admin Tenant" {
		t.Fatalf("tenant name = %q, want trimmed name", tenant.Name)
	}

	// CreateTenant inserts the subscription row in the same transaction: a
	// CLI-provisioned tenant is comped on the starter plan (design 08 "Operator
	// comps").
	var planCode string
	var comped bool
	if err := testdb.QueryRow(ctx, db, testdb.Query054, tenant.ID).Scan(&planCode, &comped); err != nil {
		t.Fatalf("load tenant subscription: %v", err)
	}
	if planCode != billing.Starter.Code {
		t.Fatalf("plan code = %q, want %q", planCode, billing.Starter.Code)
	}
	if !comped {
		t.Fatal("comped = false, want true for a CLI-provisioned tenant")
	}

	const passwordHash = "$argon2id$v=19$m=19456,t=2,p=1$ClzmGysxMTp/RFyIazZhUQ$AG2OnfvJYMcvJEC7hyKJpMH8ZCwby9D+K/Mzqb5imbg"
	user, err := admin.CreateUser(ctx, CreateUserParams{
		TenantID:     tenant.ID,
		Email:        "  Owner@Example.com  ",
		PasswordHash: passwordHash,
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

	var storedHash string
	if err := testdb.QueryRow(ctx, db, testdb.Query055, user.ID).Scan(&storedHash); err != nil {
		t.Fatalf("load user password hash: %v", err)
	}
	if storedHash != passwordHash {
		t.Fatalf("stored password hash = %q, want provided hash", storedHash)
	}

	// SubscriptionStore.GetByTenant plus the catalog resolves the entitlements
	// the RUN-5 schedule derives its interval from.
	subscriptions := NewSubscriptionStore(db)
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

	accounts := NewAccountStore(db)
	const passwordHash = "$argon2id$v=19$m=19456,t=2,p=1$ClzmGysxMTp/RFyIazZhUQ$AG2OnfvJYMcvJEC7hyKJpMH8ZCwby9D+K/Mzqb5imbg"

	tenant, user, err := accounts.CreateAccount(ctx, CreateAccountParams{
		Email:        "  Founder@Example.com  ",
		PasswordHash: passwordHash,
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query056, tenant.ID)
		_, _ = testdb.Exec(ctx, db, testdb.Query057, tenant.ID)
		_, _ = testdb.Exec(ctx, db, testdb.Query058, tenant.ID)
		_, _ = testdb.Exec(ctx, db, testdb.Query059, tenant.ID)
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
	if err := testdb.QueryRow(ctx, db, testdb.Query060, tenant.ID).Scan(&planCode, &comped, &stripeCustomerID, &stripeSubscriptionID, &stripeStatus); err != nil {
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

	businesses := NewBusinessStore(db)
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

	accounts := NewAccountStore(db)
	const passwordHash = "$argon2id$v=19$m=19456,t=2,p=1$ClzmGysxMTp/RFyIazZhUQ$AG2OnfvJYMcvJEC7hyKJpMH8ZCwby9D+K/Mzqb5imbg"

	tenant, _, err := accounts.CreateAccount(ctx, CreateAccountParams{
		Email:        "duplicate@example.com",
		PasswordHash: passwordHash,
	})
	if err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query061, tenant.ID)
		_, _ = testdb.Exec(ctx, db, testdb.Query062, tenant.ID)
		_, _ = testdb.Exec(ctx, db, testdb.Query063, tenant.ID)
	})

	var tenantsBefore int
	if err := testdb.QueryRow(ctx, db, testdb.Query064).Scan(&tenantsBefore); err != nil {
		t.Fatalf("count tenants before: %v", err)
	}

	_, _, err = accounts.CreateAccount(ctx, CreateAccountParams{
		Email:        "Duplicate@Example.com",
		PasswordHash: passwordHash,
	})
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("second CreateAccount error = %v, want ErrEmailTaken", err)
	}

	var tenantsAfter int
	if err := testdb.QueryRow(ctx, db, testdb.Query065).Scan(&tenantsAfter); err != nil {
		t.Fatalf("count tenants after: %v", err)
	}
	if tenantsAfter != tenantsBefore {
		t.Fatalf("tenant count changed from %d to %d; duplicate signup left an orphan tenant", tenantsBefore, tenantsAfter)
	}
}
