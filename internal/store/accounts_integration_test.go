package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

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
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	if tenant.Name != "Admin Tenant" {
		t.Fatalf("tenant name = %q, want trimmed name", tenant.Name)
	}

	var planSlug string
	if err := db.QueryRowContext(ctx, "SELECT slug FROM plans WHERE id = $1", tenant.PlanID).Scan(&planSlug); err != nil {
		t.Fatalf("load tenant plan: %v", err)
	}
	if planSlug != starterPlanSlug {
		t.Fatalf("plan slug = %q, want %q", planSlug, starterPlanSlug)
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
}
