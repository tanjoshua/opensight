package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
)

// TestAnalysisSchemaWipeAndRebuild exercises the ANA-1 guarantees that prose
// alone can't pin: the mentions competitor_id-iff-subject CHECK, and that the
// store's wipe primitives clear derived rows while leaving the raw
// prompt_results / monitoring_runs rows untouched.
func TestAnalysisSchemaWipeAndRebuild(t *testing.T) {
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
	promptID := mustNewID(t)
	runID := mustNewID(t)
	resultID := mustNewID(t)
	competitorID := mustNewID(t)
	selfMentionID := mustNewID(t)
	competitorMentionID := mustNewID(t)
	citationID := mustNewID(t)

	t.Cleanup(func() {
		// prompt_results / businesses cascades cover the derived rows, but delete
		// explicitly so a mid-test failure still leaves a clean table.
		_, _ = testdb.Exec(ctx, db, testdb.Query066, resultID)
		_, _ = testdb.Exec(ctx, db, testdb.Query067, resultID)
		_, _ = testdb.Exec(ctx, db, testdb.Query068, resultID)
		_, _ = testdb.Exec(ctx, db, testdb.Query069, competitorID)
		_, _ = testdb.Exec(ctx, db, testdb.Query070, resultID)
		_, _ = testdb.Exec(ctx, db, testdb.Query071, runID)
		_, _ = testdb.Exec(ctx, db, testdb.Query072, promptID)
		_, _ = testdb.Exec(ctx, db, testdb.Query073, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query074, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query075, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Analysis Tenant")
	mustExec(t, db, ctx, testdb.Query076, businessID, tenantID)
	mustExec(t, db, ctx, testdb.Query077, promptID, businessID)
	mustExec(t, db, ctx, testdb.Query078, runID, businessID)
	mustExec(t, db, ctx, testdb.Query079, resultID, runID, promptID)

	// Derived rows across all four tables — proves the schema is writable from
	// Go end to end.
	mustExec(t, db, ctx, testdb.Query080, competitorID, businessID)
	mustExec(t, db, ctx, testdb.Query081, resultID)
	mustExec(t, db, ctx, testdb.Query082, citationID, resultID)
	mustExec(t, db, ctx, testdb.Query083, selfMentionID, resultID)
	mustExec(t, db, ctx, testdb.Query084, competitorMentionID, resultID, competitorID)

	// The canonical mention invariant: competitor_id is required iff
	// subject = 'competitor'.
	if _, err := testdb.Exec(ctx, db, testdb.Query085, mustNewID(t), resultID, competitorID); err == nil {
		t.Fatal("self mention with competitor_id inserted; want CHECK violation")
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query086, mustNewID(t), resultID); err == nil {
		t.Fatal("competitor mention without competitor_id inserted; want CHECK violation")
	}

	analysisStore := NewAnalysisStore(db)

	// Wipe per-result outputs; mentions belong to the run wipe and must survive.
	if err := analysisStore.DeleteResultAnalysis(ctx, tenantID, resultID); err != nil {
		t.Fatalf("DeleteResultAnalysis: %v", err)
	}
	if got := count(t, db, ctx, testdb.Query087, resultID); got != 0 {
		t.Fatalf("result_analyses after wipe = %d, want 0", got)
	}
	if got := count(t, db, ctx, testdb.Query088, resultID); got != 0 {
		t.Fatalf("citations after wipe = %d, want 0", got)
	}
	if got := count(t, db, ctx, testdb.Query089, resultID); got != 2 {
		t.Fatalf("mentions after result wipe = %d, want 2 (untouched by result wipe)", got)
	}

	// Wipe the run's mentions.
	if err := analysisStore.DeleteRunMentions(ctx, tenantID, runID); err != nil {
		t.Fatalf("DeleteRunMentions: %v", err)
	}
	if got := count(t, db, ctx, testdb.Query090, resultID); got != 0 {
		t.Fatalf("mentions after run wipe = %d, want 0", got)
	}

	// Raw tables are never touched by the derived-data wipes.
	if got := count(t, db, ctx, testdb.Query091, resultID); got != 1 {
		t.Fatalf("prompt_results after wipes = %d, want 1 (raw untouched)", got)
	}
	if got := count(t, db, ctx, testdb.Query092, runID); got != 1 {
		t.Fatalf("monitoring_runs after wipes = %d, want 1 (raw untouched)", got)
	}
}

// TestListCompetitorsAllStatuses pins the one behavior the query prose can't:
// ListCompetitors returns competitors of every status (a dismissed competitor
// still accrues mentions, design 05/02) and never leaks another tenant's rows.
func TestListCompetitorsAllStatuses(t *testing.T) {
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
	otherBusinessID := mustNewID(t)
	discoveredID := mustNewID(t)
	trackedID := mustNewID(t)
	dismissedID := mustNewID(t)
	otherID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query093, businessID, otherBusinessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query094, businessID, otherBusinessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query095, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query096, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Competitors Tenant")
	mustExec(t, db, ctx, testdb.Query097, businessID, tenantID)
	mustExec(t, db, ctx, testdb.Query098, otherBusinessID, tenantID)

	mustExec(t, db, ctx, testdb.Query099, discoveredID, businessID)
	mustExec(t, db, ctx, testdb.Query100, trackedID, businessID)
	mustExec(t, db, ctx, testdb.Query101, dismissedID, businessID)
	// A competitor of a different business must not appear in the result.
	mustExec(t, db, ctx, testdb.Query102, otherID, otherBusinessID)

	got, err := NewAnalysisStore(db).ListCompetitors(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("ListCompetitors: %v", err)
	}

	byStatus := map[string]Competitor{}
	for _, c := range got {
		byStatus[c.Status] = c
	}
	for _, want := range []string{"discovered", "tracked", "dismissed"} {
		if _, ok := byStatus[want]; !ok {
			t.Fatalf("status %q missing from result of %d competitors", want, len(got))
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d competitors, want 3 (other business excluded)", len(got))
	}
	if aliases := byStatus["discovered"].Aliases; len(aliases) != 1 || aliases[0] != "disco" {
		t.Fatalf("discovered aliases = %v, want [disco]", aliases)
	}
}

func mustExec(t *testing.T, db *pgxpool.Pool, ctx context.Context, query testdb.Query, args ...any) {
	t.Helper()
	if _, err := testdb.Exec(ctx, db, query, args...); err != nil {
		t.Fatalf("exec test query %d: %v", query, err)
	}
}

func count(t *testing.T, db *pgxpool.Pool, ctx context.Context, query testdb.Query, args ...any) int {
	t.Helper()
	var n int
	if err := testdb.QueryRow(ctx, db, query, args...).Scan(&n); err != nil {
		t.Fatalf("count test query %d: %v", query, err)
	}
	return n
}
