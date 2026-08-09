package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
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

	accountID := mustNewID(t)
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
		_, _ = db.Exec(ctx, "DELETE FROM mentions WHERE prompt_result_id = $1", resultID)
		_, _ = db.Exec(ctx, "DELETE FROM citations WHERE prompt_result_id = $1", resultID)
		_, _ = db.Exec(ctx, "DELETE FROM result_analyses WHERE prompt_result_id = $1", resultID)
		_, _ = db.Exec(ctx, "DELETE FROM competitors WHERE id = $1", competitorID)
		_, _ = db.Exec(ctx, "DELETE FROM prompt_results WHERE id = $1", resultID)
		_, _ = db.Exec(ctx, "DELETE FROM monitoring_runs WHERE id = $1", runID)
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE id = $1", promptID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Analysis Account")
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Analysis Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`, businessID, accountID)
	mustExec(t, db, ctx, "INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic near me', 'active')", promptID, businessID)
	mustExec(t, db, ctx, `
		INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at)
		VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'analysis-workflow', now())`, runID, businessID)
	mustExec(t, db, ctx, `
		INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
		VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_1"}'::jsonb, 'Analysis Clinic and Rival Clinic are options.')`, resultID, runID, promptID)

	// Derived rows and their link table — proves the schema is writable from
	// Go end to end.
	mustExec(t, db, ctx, `
		INSERT INTO competitors (id, business_id, name, aliases, source, status)
		VALUES ($1, $2, 'Rival Clinic', ARRAY['rival clinic']::text[], 'discovered', 'discovered')`, competitorID, businessID)
	mustExec(t, db, ctx, `
		INSERT INTO result_analyses (prompt_result_id, sentiment, keywords, excerpts, analysis_model, extraction_version)
		VALUES ($1, 'positive', ARRAY['friendly']::text[], '["Analysis Clinic is a good option."]'::jsonb, 'gpt-5-mini', 1)`, resultID)
	mustExec(t, db, ctx, `
		INSERT INTO citations (id, prompt_result_id, url, domain, subject, cite_order, text_start, text_end)
		VALUES ($1, $2, 'https://example.com/x', 'example.com', 'business', 0, 0, 10)`, citationID, resultID)
	mustExec(t, db, ctx, `
		INSERT INTO mentions (id, prompt_result_id, subject, matched_by, mention_order, excerpt)
		VALUES ($1, $2, 'self', 'exact', 0, 'Analysis Clinic ... options.')`, selfMentionID, resultID)
	mustExec(t, db, ctx, `
		INSERT INTO mentions (id, prompt_result_id, subject, competitor_id, matched_by, mention_order, excerpt)
		VALUES ($1, $2, 'competitor', $3, 'exact', 1, 'Rival Clinic ... options.')`, competitorMentionID, resultID, competitorID)
	mustExec(t, db, ctx, `INSERT INTO mention_citations (mention_id,citation_id) VALUES ($1,$2),($3,$2)`,
		selfMentionID, citationID, competitorMentionID)

	// The canonical mention invariant: competitor_id is required iff
	// subject = 'competitor'.
	if _, err := db.Exec(ctx, `
		INSERT INTO mentions (id, prompt_result_id, subject, competitor_id, matched_by, mention_order, excerpt)
		VALUES ($1, $2, 'self', $3, 'exact', 2, 'bad')`, mustNewID(t), resultID, competitorID); err == nil {
		t.Fatal("self mention with competitor_id inserted; want CHECK violation")
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO mentions (id, prompt_result_id, subject, matched_by, mention_order, excerpt)
		VALUES ($1, $2, 'competitor', 'exact', 2, 'bad')`, mustNewID(t), resultID); err == nil {
		t.Fatal("competitor mention without competitor_id inserted; want CHECK violation")
	}

	analysisStore := New(db)

	// Wipe per-result outputs; mentions belong to the run wipe and must survive.
	if err := analysisStore.DeleteResultAnalysis(ctx, accountID, resultID); err != nil {
		t.Fatalf("DeleteResultAnalysis: %v", err)
	}
	if got := count(t, db, ctx, "SELECT count(*) FROM result_analyses WHERE prompt_result_id = $1", resultID); got != 0 {
		t.Fatalf("result_analyses after wipe = %d, want 0", got)
	}
	if got := count(t, db, ctx, "SELECT count(*) FROM citations WHERE prompt_result_id = $1", resultID); got != 0 {
		t.Fatalf("citations after wipe = %d, want 0", got)
	}
	if got := count(t, db, ctx, `SELECT count(*) FROM mention_citations WHERE mention_id IN ($1,$2)`, selfMentionID, competitorMentionID); got != 0 {
		t.Fatalf("mention citation links after citation wipe = %d, want 0 (cascade)", got)
	}
	if got := count(t, db, ctx, "SELECT count(*) FROM mentions WHERE prompt_result_id = $1", resultID); got != 2 {
		t.Fatalf("mentions after result wipe = %d, want 2 (untouched by result wipe)", got)
	}

	// Wipe the run's mentions.
	if err := analysisStore.DeleteRunMentions(ctx, accountID, runID); err != nil {
		t.Fatalf("DeleteRunMentions: %v", err)
	}
	if got := count(t, db, ctx, "SELECT count(*) FROM mentions WHERE prompt_result_id = $1", resultID); got != 0 {
		t.Fatalf("mentions after run wipe = %d, want 0", got)
	}

	// Raw tables are never touched by the derived-data wipes.
	if got := count(t, db, ctx, "SELECT count(*) FROM prompt_results WHERE id = $1", resultID); got != 1 {
		t.Fatalf("prompt_results after wipes = %d, want 1 (raw untouched)", got)
	}
	if got := count(t, db, ctx, "SELECT count(*) FROM monitoring_runs WHERE id = $1", runID); got != 1 {
		t.Fatalf("monitoring_runs after wipes = %d, want 1 (raw untouched)", got)
	}
}

// TestListCompetitorsAllStatuses pins the one behavior the query prose can't:
// ListCompetitors returns competitors of every status (a dismissed competitor
// still accrues mentions, design 05/02) and never leaks another account's rows.
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

	accountID := mustNewID(t)
	businessID := mustNewID(t)
	otherBusinessID := mustNewID(t)
	discoveredID := mustNewID(t)
	trackedID := mustNewID(t)
	dismissedID := mustNewID(t)
	otherID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM competitors WHERE business_id IN ($1, $2)", businessID, otherBusinessID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id IN ($1, $2)", businessID, otherBusinessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Competitors Account")
	mustExec(t, db, ctx, "INSERT INTO businesses (id, account_id, status, name) VALUES ($1, $2, 'draft', 'Atlas Clinic')", businessID, accountID)
	mustExec(t, db, ctx, "INSERT INTO businesses (id, account_id, status, name) VALUES ($1, $2, 'draft', 'Other Clinic')", otherBusinessID, accountID)

	mustExec(t, db, ctx, `
		INSERT INTO competitors (id, business_id, name, aliases, source, status)
		VALUES ($1, $2, 'Discovered Co', ARRAY['disco']::text[], 'discovered', 'discovered')`, discoveredID, businessID)
	mustExec(t, db, ctx, `
		INSERT INTO competitors (id, business_id, name, source, status)
		VALUES ($1, $2, 'Tracked Co', 'manual', 'tracked')`, trackedID, businessID)
	mustExec(t, db, ctx, `
		INSERT INTO competitors (id, business_id, name, source, status)
		VALUES ($1, $2, 'Dismissed Co', 'discovered', 'dismissed')`, dismissedID, businessID)
	// A competitor of a different business must not appear in the result.
	mustExec(t, db, ctx, `
		INSERT INTO competitors (id, business_id, name, source, status)
		VALUES ($1, $2, 'Other Co', 'discovered', 'discovered')`, otherID, otherBusinessID)

	got, err := New(db).ListCompetitors(ctx, accountID, businessID)
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

func mustExec(t *testing.T, db *pgxpool.Pool, ctx context.Context, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}

func count(t *testing.T, db *pgxpool.Pool, ctx context.Context, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", query, err)
	}
	return n
}
