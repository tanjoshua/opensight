package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"

	"opensight/internal/billing"
	"opensight/internal/domain"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// fillActivePrompts creates n active prompts with distinct text, up to (and
// possibly at) the catalog's starter prompt limit, so limit-boundary tests
// don't hardcode a literal.
func fillActivePrompts(t *testing.T, ctx context.Context, promptStore *PromptStore, tenantID, businessID domain.ID, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := promptStore.CreateActivePrompt(ctx, CreateActivePromptParams{
			TenantID:   tenantID,
			BusinessID: businessID,
			Text:       fmt.Sprintf("filler prompt %d", i),
		}); err != nil {
			t.Fatalf("create filler prompt %d: %v", i, err)
		}
	}
}

func TestPromptStoreCreateActivePromptHonorsPlanLimit(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	tenantID := mustNewID(t)
	businessID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM prompts WHERE business_id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM subscriptions WHERE tenant_id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Prompt Limit Tenant")
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Prompt Limit Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`,
		businessID,
		tenantID,
	); err != nil {
		t.Fatalf("insert test business: %v", err)
	}

	promptStore := NewPromptStore(db)
	fillActivePrompts(t, ctx, promptStore, tenantID, businessID, billing.Starter.PromptLimit)

	_, err = promptStore.CreateActivePrompt(ctx, CreateActivePromptParams{
		TenantID:   tenantID,
		BusinessID: businessID,
		Text:       "where should I book a clinic appointment",
	})
	if !errors.Is(err, ErrPromptLimitExceeded) {
		t.Fatalf("prompt past the limit error = %v, want ErrPromptLimitExceeded", err)
	}

	var activePromptCount int
	if err := db.QueryRowContext(
		ctx,
		`SELECT count(*) FROM prompts WHERE business_id = $1 AND status = 'active'`,
		businessID,
	).Scan(&activePromptCount); err != nil {
		t.Fatalf("count active prompts: %v", err)
	}
	if activePromptCount != billing.Starter.PromptLimit {
		t.Fatalf("active prompt count = %d, want %d", activePromptCount, billing.Starter.PromptLimit)
	}
}

// TestPromptStoreReplacePrompt exercises the retire+insert transaction against
// real constraints: the old prompt is retired (not counted toward the limit), the
// new active prompt records replaces_prompt_id, and a second replace of the now
// retired prompt is rejected as ErrPromptNotActive.
func TestPromptStoreReplacePrompt(t *testing.T) {
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
	businessID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM prompts WHERE business_id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM subscriptions WHERE tenant_id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Replace Tenant")
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Replace Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`,
		businessID, tenantID,
	); err != nil {
		t.Fatalf("insert test business: %v", err)
	}

	promptStore := NewPromptStore(db)
	// Fill to the catalog's starter limit minus one, then create the original as
	// the last slot: the replace below only fits because retiring the original
	// frees its slot before the insert counts (at the limit boundary).
	fillActivePrompts(t, ctx, promptStore, tenantID, businessID, billing.Starter.PromptLimit-1)
	original, err := promptStore.CreateActivePrompt(ctx, CreateActivePromptParams{
		TenantID:   tenantID,
		BusinessID: businessID,
		Text:       "best clinic near me",
	})
	if err != nil {
		t.Fatalf("create original prompt: %v", err)
	}

	replacement, err := promptStore.ReplacePrompt(ctx, ReplacePromptParams{
		TenantID:    tenantID,
		OldPromptID: original.ID,
		Text:        "top rated clinic nearby",
	})
	if err != nil {
		t.Fatalf("replace prompt: %v", err)
	}
	if replacement.ReplacesPromptID == nil || *replacement.ReplacesPromptID != original.ID {
		t.Fatalf("replaces prompt id = %v, want %s", replacement.ReplacesPromptID, original.ID)
	}
	if replacement.Status != PromptStatusActive {
		t.Fatalf("replacement status = %q, want active", replacement.Status)
	}

	var oldStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM prompts WHERE id = $1`, original.ID).Scan(&oldStatus); err != nil {
		t.Fatalf("load old prompt status: %v", err)
	}
	if oldStatus != string(PromptStatusRetired) {
		t.Fatalf("old prompt status = %q, want retired", oldStatus)
	}

	// Replacing the now-retired original is rejected.
	if _, err := promptStore.ReplacePrompt(ctx, ReplacePromptParams{
		TenantID:    tenantID,
		OldPromptID: original.ID,
		Text:        "another prompt",
	}); !errors.Is(err, ErrPromptNotActive) {
		t.Fatalf("replace retired prompt error = %v, want ErrPromptNotActive", err)
	}

	var activeCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM prompts WHERE business_id = $1 AND status = 'active'`, businessID).Scan(&activeCount); err != nil {
		t.Fatalf("count active prompts: %v", err)
	}
	if activeCount != billing.Starter.PromptLimit {
		t.Fatalf("active prompt count = %d, want %d", activeCount, billing.Starter.PromptLimit)
	}
}

func mustNewID(t *testing.T) domain.ID {
	t.Helper()

	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("new ID: %v", err)
	}
	return id
}
