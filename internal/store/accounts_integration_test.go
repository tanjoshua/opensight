package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"opensight/internal/billing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestAdminStoreCreateTenantAndUser(t *testing.T) {
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

	admin := NewAdminStore(db)
	tenant, err := admin.CreateTenant(ctx, CreateTenantParams{Name: "  Admin Tenant  "})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM users WHERE tenant_id = $1", tenant.ID)
		_, _ = db.ExecContext(ctx, "DELETE FROM subscriptions WHERE tenant_id = $1", tenant.ID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	if tenant.Name != "Admin Tenant" {
		t.Fatalf("tenant name = %q, want trimmed name", tenant.Name)
	}

	// CreateTenant inserts the subscription row in the same transaction: a
	// CLI-provisioned tenant is comped on the starter plan (design 08 "Operator
	// comps").
	var planCode string
	var comped bool
	if err := db.QueryRowContext(ctx, "SELECT plan_code, comped FROM subscriptions WHERE tenant_id = $1", tenant.ID).Scan(&planCode, &comped); err != nil {
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
	if err := db.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id = $1", user.ID).Scan(&storedHash); err != nil {
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
