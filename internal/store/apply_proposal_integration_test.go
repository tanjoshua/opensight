package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
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
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	tenantID := mustNewID(t)
	businessID := mustNewID(t)
	manualBusinessID := mustNewID(t)
	proposalID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query103, businessID, manualBusinessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query104, businessID, manualBusinessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query105, businessID, manualBusinessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query106, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query107, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Apply Tenant")
	mustExec(t, db, ctx, testdb.Query108, businessID, tenantID)
	mustExec(t, db, ctx, testdb.Query109, proposalID, businessID)

	applyStore := NewApplyProposalStore(db)

	result, err := applyStore.Apply(ctx, ApplyProposalParams{
		TenantID:    tenantID,
		BusinessID:  businessID,
		Name:        "Draft Clinic",
		Aliases:     []string{"DC Ortho"},
		Category:    "orthopaedic clinic",
		Services:    json.RawMessage(`["ACL reconstruction"]`),
		Location:    json.RawMessage(`{"city":"Singapore","country":"SG"}`),
		PromptTexts: []string{"best orthopaedic clinic in Singapore", "who fixes knees near Novena"},
		ActivatedAt: time.Now().UTC(),
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
	mustScan(t, db, ctx, testdb.Query284, &activeCount, businessID)
	if activeCount != 2 {
		t.Fatalf("active prompts = %d, want 2", activeCount)
	}
	var proposalStatus string
	mustScan(t, db, ctx, testdb.Query285, &proposalStatus, proposalID)
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
	mustExec(t, db, ctx, testdb.Query110, manualBusinessID, tenantID)
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

func mustScan(t *testing.T, db *pgxpool.Pool, ctx context.Context, query testdb.Query, dest any, args ...any) {
	t.Helper()
	if err := testdb.QueryRow(ctx, db, query, args...).Scan(dest); err != nil {
		t.Fatalf("scan test query %d: %v", query, err)
	}
}
