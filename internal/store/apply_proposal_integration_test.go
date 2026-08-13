package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

	accountID := mustNewID(t)
	businessID := mustNewID(t)
	manualBusinessID := mustNewID(t)
	proposalID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE business_id IN ($1, $2)", businessID, manualBusinessID)
		_, _ = db.Exec(ctx, "DELETE FROM profile_proposals WHERE business_id IN ($1, $2)", businessID, manualBusinessID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id IN ($1, $2)", businessID, manualBusinessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Apply Account")
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name, website)
		VALUES ($1, $2, 'draft', 'Draft Clinic', 'https://draft.example')`, businessID, accountID)
	mustExec(t, db, ctx, `
		INSERT INTO profile_proposals (id, business_id, payload, status)
		VALUES ($1, $2, '{"low_confidence":false}'::jsonb, 'pending')`, proposalID, businessID)

	applyStore := New(db)

	result, err := applyStore.Apply(ctx, ApplyProposalParams{
		AccountID:   accountID,
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
	mustScan(t, db, ctx, "SELECT count(*) FROM prompts WHERE business_id = $1 AND status = 'active'", &activeCount, businessID)
	if activeCount != 2 {
		t.Fatalf("active prompts = %d, want 2", activeCount)
	}
	var proposalStatus string
	mustScan(t, db, ctx, "SELECT status FROM profile_proposals WHERE id = $1", &proposalStatus, proposalID)
	if proposalStatus != "applied" {
		t.Fatalf("proposal status = %q, want applied", proposalStatus)
	}

	// A second apply on the now-active business is rejected.
	_, err = applyStore.Apply(ctx, ApplyProposalParams{
		AccountID:   accountID,
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
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name)
		VALUES ($1, $2, 'draft', 'Manual Clinic')`, manualBusinessID, accountID)
	manual, err := applyStore.Apply(ctx, ApplyProposalParams{
		AccountID:   accountID,
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

func mustScan(t *testing.T, db *pgxpool.Pool, ctx context.Context, query string, dest any, args ...any) {
	t.Helper()
	if err := db.QueryRow(ctx, query, args...).Scan(dest); err != nil {
		t.Fatalf("scan %s: %v", query, err)
	}
}
