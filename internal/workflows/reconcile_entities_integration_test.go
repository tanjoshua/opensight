package workflows

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// fakeMatcher resolves a verbatim name to a competitor by NAME using whatever
// candidate list ReconcileEntities loads fresh from the DB, so the test does not
// need to know the competitor id ahead of time. Names absent from the map return
// null (no match).
type fakeMatcher struct {
	matchNameToCompetitor map[string]string
	calls                 int
}

func (f *fakeMatcher) RunMatch(_ context.Context, in llm.MatchInput) (llm.MatchRunResult, error) {
	f.calls++
	idByName := map[string]string{}
	for _, c := range in.Competitors {
		idByName[c.Name] = c.ID.String()
	}
	type entry struct {
		Index        int     `json:"index"`
		CompetitorID *string `json:"competitor_id"`
	}
	entries := make([]entry, len(in.Names))
	for i, n := range in.Names {
		e := entry{Index: i}
		if target, ok := f.matchNameToCompetitor[n]; ok {
			if idStr, ok := idByName[target]; ok {
				id := idStr
				e.CompetitorID = &id
			}
		}
		entries[i] = e
	}
	raw, err := json.Marshal(map[string]any{"matches": entries})
	if err != nil {
		return llm.MatchRunResult{}, err
	}
	return llm.MatchRunResult{RawJSON: raw, Model: "fake-match"}, nil
}

func TestReconcileEntitiesAgainstPostgres(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run activity integration tests")
	}

	ctx := context.Background()
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	planID := mustID(t)
	tenantID := mustID(t)
	businessID := mustID(t)
	promptID := mustID(t)
	runID := mustID(t)
	resultID := mustID(t)
	bravoID := mustID(t)
	slug := "reconcile-" + planID.String()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM mentions WHERE prompt_result_id = $1", resultID)
		_, _ = db.ExecContext(ctx, "DELETE FROM competitors WHERE business_id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM prompt_results WHERE id = $1", resultID)
		_, _ = db.ExecContext(ctx, "DELETE FROM monitoring_runs WHERE id = $1", runID)
		_, _ = db.ExecContext(ctx, "DELETE FROM prompts WHERE id = $1", promptID)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM plans WHERE id = $1", planID)
	})

	mustExec(t, db, ctx,
		`INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ($1, $2, 5, 'test', ARRAY['chatgpt']::text[])`, planID, slug)
	mustExec(t, db, ctx,
		`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Reconcile Tenant', $2)`, tenantID, planID)
	mustExec(t, db, ctx,
		`INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Atlas Dental', 'clinic', '{"country":"SG"}'::jsonb, now())`, businessID, tenantID)
	mustExec(t, db, ctx,
		`INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic', 'active')`, promptID, businessID)
	// completed_at must be set: the analysis_completed_at CHECK forbids stamping
	// analysis before the run is marked complete (FinalizeRun sets it in prod).
	mustExec(t, db, ctx,
		`INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'reconcile-wf', now())`, runID, businessID)
	mustExec(t, db, ctx,
		`INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5', '{"model":"gpt-5"}'::jsonb, '{"id":"r"}'::jsonb, 'text')`,
		resultID, runID, promptID)
	// An existing competitor for the exact + LLM passes to match against.
	mustExec(t, db, ctx,
		`INSERT INTO competitors (id, business_id, name, aliases, source, status)
VALUES ($1, $2, 'Bravo Clinic', ARRAY['bravo clinic']::text[], 'manual', 'tracked')`, bravoID, businessID)

	businesses := store.NewBusinessStore(db)
	analysis := store.NewAnalysisStore(db)
	matcher := &fakeMatcher{matchNameToCompetitor: map[string]string{"Bravo Klinik": "Bravo Clinic"}}
	acts := NewActivities(businesses, nil, nil, nil, nil, analysis, nil, matcher, nil, nil)

	in := ReconcileEntitiesInput{
		TenantID:   tenantID,
		BusinessID: businessID,
		RunID:      runID,
		Results: []ResultEntities{{
			ResultID: resultID,
			Entities: []llm.ExtractedEntity{
				{VerbatimName: "Atlas Dental", IsTarget: true, Excerpt: "Atlas Dental is great."},
				{VerbatimName: "Bravo Clinic", Excerpt: "Bravo Clinic is nearby."},
				{VerbatimName: "Bravo Klinik", Excerpt: "Bravo Klinik also listed."},
				{VerbatimName: "Charlie Medical", Excerpt: "Charlie Medical rounds it out."},
			},
		}},
	}

	out, err := acts.ReconcileEntities(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileEntities: %v", err)
	}
	if out.MentionsWritten != 4 || out.DiscoveredCreated != 1 || out.LLMMatched != 1 {
		t.Fatalf("output = %+v, want {4, 1, 1}", out)
	}

	// matched_by is correct per subject/pass.
	assertMention(t, db, ctx, resultID, "self", 0, "exact", "Atlas Dental")
	assertMention(t, db, ctx, resultID, "competitor", 1, "exact", "Bravo Clinic")    // exact
	assertMention(t, db, ctx, resultID, "competitor", 2, "llm", "Bravo Klinik")      // llm
	assertMention(t, db, ctx, resultID, "competitor", 3, "exact", "Charlie Medical") // discovered, own alias exact

	// The discovered competitor exists with the verbatim name as its sole alias.
	var charlieStatus, charlieSource string
	if err := db.QueryRowContext(ctx,
		`SELECT status, source FROM competitors WHERE business_id = $1 AND name = 'Charlie Medical'`,
		businessID).Scan(&charlieStatus, &charlieSource); err != nil {
		t.Fatalf("read discovered competitor: %v", err)
	}
	if charlieStatus != "discovered" || charlieSource != "discovered" {
		t.Errorf("discovered competitor = (%q, %q), want (discovered, discovered)", charlieStatus, charlieSource)
	}
	charlieAliases := textArray(t, db, ctx,
		`SELECT to_jsonb(aliases) FROM competitors WHERE business_id = $1 AND name = 'Charlie Medical'`, businessID)
	if len(charlieAliases) != 1 || charlieAliases[0] != "Charlie Medical" {
		t.Errorf("discovered aliases = %v, want [Charlie Medical]", charlieAliases)
	}

	// The LLM variant landed in suggested_aliases, never promoted to aliases.
	bravoSuggested := textArray(t, db, ctx, `SELECT to_jsonb(suggested_aliases) FROM competitors WHERE id = $1`, bravoID)
	bravoAliases := textArray(t, db, ctx, `SELECT to_jsonb(aliases) FROM competitors WHERE id = $1`, bravoID)
	if countOccurrences(bravoSuggested, "Bravo Klinik") == 0 {
		t.Errorf("bravo suggested_aliases = %v, want to contain Bravo Klinik", bravoSuggested)
	}
	if countOccurrences(bravoAliases, "Bravo Klinik") != 0 {
		t.Errorf("bravo aliases = %v, must NOT contain the unapproved variant", bravoAliases)
	}

	// Commit stamped analysis_completed_at.
	var analysisCompleted sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT analysis_completed_at FROM monitoring_runs WHERE id = $1`, runID).Scan(&analysisCompleted); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if !analysisCompleted.Valid {
		t.Error("analysis_completed_at not set")
	}

	// Idempotency: a full re-run reloads competitors fresh, so Charlie now exact-
	// matches its own minted alias (no second discovered row), Bravo Klinik is
	// re-judged and its suggested alias append is a no-op, and mentions are
	// delete-and-rewritten (still 4).
	out2, err := acts.ReconcileEntities(ctx, in)
	if err != nil {
		t.Fatalf("second ReconcileEntities: %v", err)
	}
	if out2.DiscoveredCreated != 0 {
		t.Errorf("second run DiscoveredCreated = %d, want 0 (Charlie already exists)", out2.DiscoveredCreated)
	}
	if got := countRows(t, db, ctx, `SELECT count(*) FROM competitors WHERE business_id = $1 AND name = 'Charlie Medical'`, businessID); got != 1 {
		t.Errorf("Charlie competitor rows after rerun = %d, want 1 (no duplicate)", got)
	}
	if got := countRows(t, db, ctx, `SELECT count(*) FROM mentions WHERE prompt_result_id = $1`, resultID); got != 4 {
		t.Errorf("mentions after rerun = %d, want 4 (delete-and-rewrite)", got)
	}
	bravoSuggested = textArray(t, db, ctx, `SELECT to_jsonb(suggested_aliases) FROM competitors WHERE id = $1`, bravoID)
	if n := countOccurrences(bravoSuggested, "Bravo Klinik"); n != 1 {
		t.Errorf("Bravo Klinik appears %d times in suggested_aliases, want 1 (append idempotent)", n)
	}
}

func assertMention(t *testing.T, db *sql.DB, ctx context.Context, resultID domain.ID, subject string, order int, wantMatchedBy, wantVerbatim string) {
	t.Helper()
	var matchedBy, verbatimName string
	if err := db.QueryRowContext(ctx,
		`SELECT matched_by, verbatim_name FROM mentions WHERE prompt_result_id = $1 AND subject = $2 AND mention_order = $3`,
		resultID, subject, order).Scan(&matchedBy, &verbatimName); err != nil {
		t.Fatalf("read mention (subject=%s order=%d): %v", subject, order, err)
	}
	if matchedBy != wantMatchedBy {
		t.Errorf("mention (subject=%s order=%d) matched_by = %q, want %q", subject, order, matchedBy, wantMatchedBy)
	}
	if verbatimName != wantVerbatim {
		t.Errorf("mention (subject=%s order=%d) verbatim_name = %q, want %q", subject, order, verbatimName, wantVerbatim)
	}
}

func countRows(t *testing.T, db *sql.DB, ctx context.Context, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// textArray reads a single to_jsonb(text[]) column into []string.
func textArray(t *testing.T, db *sql.DB, ctx context.Context, query string, args ...any) []string {
	t.Helper()
	var raw []byte
	if err := db.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		t.Fatalf("read text array: %v", err)
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal text array %q: %v", raw, err)
	}
	return out
}

func countOccurrences(arr []string, want string) int {
	n := 0
	for _, s := range arr {
		if s == want {
			n++
		}
	}
	return n
}
