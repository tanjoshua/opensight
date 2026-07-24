package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"opensight/internal/domain"

	_ "github.com/jackc/pgx/v5/stdlib"
)

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

	planID := mustNewID(t)
	tenantID := mustNewID(t)
	businessID := mustNewID(t)
	slug := "prompt-limit-" + planID.String()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM plans WHERE id = $1", planID)
	})

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ($1, $2, 1, 'test', ARRAY['chatgpt']::text[])`,
		planID,
		slug,
	); err != nil {
		t.Fatalf("insert test plan: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO tenants (id, name, plan_id)
VALUES ($1, 'Prompt Limit Tenant', $2)`,
		tenantID,
		planID,
	); err != nil {
		t.Fatalf("insert test tenant: %v", err)
	}
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
	if _, err := promptStore.CreateActivePrompt(ctx, CreateActivePromptParams{
		TenantID:   tenantID,
		BusinessID: businessID,
		Text:       "best clinic near me",
	}); err != nil {
		t.Fatalf("create first prompt: %v", err)
	}

	_, err = promptStore.CreateActivePrompt(ctx, CreateActivePromptParams{
		TenantID:   tenantID,
		BusinessID: businessID,
		Text:       "where should I book a clinic appointment",
	})
	if !errors.Is(err, ErrPromptLimitExceeded) {
		t.Fatalf("second prompt error = %v, want ErrPromptLimitExceeded", err)
	}

	var activePromptCount int
	if err := db.QueryRowContext(
		ctx,
		`SELECT count(*) FROM prompts WHERE business_id = $1 AND status = 'active'`,
		businessID,
	).Scan(&activePromptCount); err != nil {
		t.Fatalf("count active prompts: %v", err)
	}
	if activePromptCount != 1 {
		t.Fatalf("active prompt count = %d, want 1", activePromptCount)
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

	planID := mustNewID(t)
	tenantID := mustNewID(t)
	businessID := mustNewID(t)
	slug := "prompt-replace-" + planID.String()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM prompts WHERE business_id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM plans WHERE id = $1", planID)
	})

	// prompt_limit 1: the replace only fits because retiring the old prompt frees
	// the single slot before the insert counts.
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ($1, $2, 1, 'test', ARRAY['chatgpt']::text[])`,
		planID, slug,
	); err != nil {
		t.Fatalf("insert test plan: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Replace Tenant', $2)`,
		tenantID, planID,
	); err != nil {
		t.Fatalf("insert test tenant: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Replace Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`,
		businessID, tenantID,
	); err != nil {
		t.Fatalf("insert test business: %v", err)
	}

	promptStore := NewPromptStore(db)
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
	if activeCount != 1 {
		t.Fatalf("active prompt count = %d, want 1", activeCount)
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
