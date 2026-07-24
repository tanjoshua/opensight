package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestApplyProposalStoreActivatesBusiness exercises Apply against real Postgres
// so the businesses_active_profile_check, the prompt plan-limit enforcement, and
// the proposal state transition are all validated against actual constraints.
func TestApplyProposalStoreActivatesBusiness(t *testing.T) {
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
	manualBusinessID := mustNewID(t)
	proposalID := mustNewID(t)
	slug := "apply-proposal-" + planID.String()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM prompts WHERE business_id IN ($1, $2)", businessID, manualBusinessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM profile_proposals WHERE business_id IN ($1, $2)", businessID, manualBusinessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id IN ($1, $2)", businessID, manualBusinessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM plans WHERE id = $1", planID)
	})

	mustExec(t, db, ctx,
		`INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ($1, $2, 2, 'weekly', ARRAY['chatgpt']::text[])`, planID, slug)
	mustExec(t, db, ctx,
		`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Apply Tenant', $2)`, tenantID, planID)
	mustExec(t, db, ctx,
		`INSERT INTO businesses (id, tenant_id, status, name, website)
VALUES ($1, $2, 'draft', 'Draft Clinic', 'https://draft.example')`, businessID, tenantID)
	mustExec(t, db, ctx,
		`INSERT INTO profile_proposals (id, business_id, payload, status)
VALUES ($1, $2, '{"low_confidence":false}'::jsonb, 'pending')`, proposalID, businessID)

	applyStore := NewApplyProposalStore(db)

	result, err := applyStore.Apply(ctx, ApplyProposalParams{
		TenantID:      tenantID,
		BusinessID:    businessID,
		Name:          "Draft Clinic",
		Aliases:       []string{"DC Ortho"},
		Category:      "orthopaedic clinic",
		Practitioners: json.RawMessage(`[{"name":"Dr Tan","role":"surgeon"}]`),
		Services:      json.RawMessage(`["ACL reconstruction"]`),
		Location:      json.RawMessage(`{"city":"Singapore","country":"SG"}`),
		PromptTexts:   []string{"best orthopaedic clinic in Singapore", "who fixes knees near Novena"},
		ActivatedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.Business.Status != BusinessStatusActive {
		t.Fatalf("business status = %q, want active", result.Business.Status)
	}
	if result.Business.Category == nil || *result.Business.Category != "orthopaedic clinic" {
		t.Fatalf("category = %v, want orthopaedic clinic", result.Business.Category)
	}
	if result.Business.ActivatedAt == nil {
		t.Fatal("activated_at not set")
	}
	if len(result.Business.Aliases) != 1 || result.Business.Aliases[0] != "DC Ortho" {
		t.Fatalf("aliases = %v, want [DC Ortho]", result.Business.Aliases)
	}
	if result.Business.Website == nil || *result.Business.Website != "https://draft.example" {
		t.Fatalf("website = %v, want the value set at creation", result.Business.Website)
	}
	if len(result.Prompts) != 2 {
		t.Fatalf("prompts = %d, want 2", len(result.Prompts))
	}

	var activeCount int
	mustScan(t, db, ctx, `SELECT count(*) FROM prompts WHERE business_id = $1 AND status = 'active'`, &activeCount, businessID)
	if activeCount != 2 {
		t.Fatalf("active prompts = %d, want 2", activeCount)
	}
	var proposalStatus string
	mustScan(t, db, ctx, `SELECT status FROM profile_proposals WHERE id = $1`, &proposalStatus, proposalID)
	if proposalStatus != "applied" {
		t.Fatalf("proposal status = %q, want applied", proposalStatus)
	}

	// A second apply on the now-active business is rejected.
	_, err = applyStore.Apply(ctx, ApplyProposalParams{
		TenantID:    tenantID,
		BusinessID:  businessID,
		Name:        "Draft Clinic",
		Category:    "orthopaedic clinic",
		Location:    json.RawMessage(`{"country":"SG"}`),
		PromptTexts: []string{"anything"},
		ActivatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrBusinessNotDraft) {
		t.Fatalf("second apply error = %v, want ErrBusinessNotDraft", err)
	}

	// Manual-setup path: a draft business with no pending proposal still applies.
	mustExec(t, db, ctx,
		`INSERT INTO businesses (id, tenant_id, status, name)
VALUES ($1, $2, 'draft', 'Manual Clinic')`, manualBusinessID, tenantID)
	manual, err := applyStore.Apply(ctx, ApplyProposalParams{
		TenantID:    tenantID,
		BusinessID:  manualBusinessID,
		Name:        "Manual Clinic",
		Category:    "physiotherapy",
		Location:    json.RawMessage(`{"country":"SG"}`),
		PromptTexts: []string{"best physio in Singapore"},
		ActivatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("manual apply: %v", err)
	}
	if manual.Business.Status != BusinessStatusActive {
		t.Fatalf("manual business status = %q, want active", manual.Business.Status)
	}
}

func mustScan(t *testing.T, db *sql.DB, ctx context.Context, query string, dest any, args ...any) {
	t.Helper()
	if err := db.QueryRowContext(ctx, query, args...).Scan(dest); err != nil {
		t.Fatalf("scan %q: %v", query, err)
	}
}
