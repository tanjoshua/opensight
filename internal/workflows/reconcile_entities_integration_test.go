package workflows

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
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
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run operation integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	accountID := mustID(t)
	businessID := mustID(t)
	promptID := mustID(t)
	runID := mustID(t)
	resultID := mustID(t)
	bravoID := mustID(t)
	atlasCiteID, dirCiteID := mustID(t), mustID(t)

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM mentions WHERE prompt_result_id = $1", resultID)
		_, _ = db.Exec(ctx, "DELETE FROM competitors WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM prompt_results WHERE id = $1", resultID)
		_, _ = db.Exec(ctx, "DELETE FROM monitoring_runs WHERE id = $1", runID)
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE id = $1", promptID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Reconcile Account")
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Atlas Dental', 'clinic', '{"country":"SG"}'::jsonb, now())`, businessID, accountID)
	mustExec(t, db, ctx, "INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic', 'active')", promptID, businessID)
	// completed_at must be set: the analysis_completed_at CHECK forbids stamping
	// analysis before the run is marked complete (FinalizeRun sets it in prod).
	mustExec(t, db, ctx, `
		INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, job_id, completed_at)
		VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 201, now())`, runID, businessID)
	mustExec(t, db, ctx, `
		INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
		VALUES ($1, $2, $3, 'succeeded', 'gpt-5', '{"model":"gpt-5"}'::jsonb, '{"id":"r"}'::jsonb,
		'Atlas Dental is great. Bravo Clinic and Charlie Medical are recommended.')`, resultID, runID, promptID)
	// Two citations for the result, as phase 1 would have written them, so the
	// mentions phase 2 writes can resolve their source by cite_order.
	mustExec(t, db, ctx, `
		INSERT INTO citations (id, prompt_result_id, url, domain, title, cite_order, subject, text_start, text_end)
		VALUES ($1, $3, 'https://atlas.example/a', 'atlas.example', NULL, 0, 'business', 0, 22),
		       ($2, $3, 'https://dir.example/b', 'dir.example', NULL, 1, 'competitor', 23, 72)`,
		atlasCiteID, dirCiteID, resultID)
	// An existing competitor for the exact + LLM passes to match against.
	mustExec(t, db, ctx, `
		INSERT INTO competitors (id, business_id, name, aliases, source, status)
		VALUES ($1, $2, 'Bravo Clinic', ARRAY['bravo clinic']::text[], 'manual', 'tracked')`, bravoID, businessID)

	analysisStore := store.New(db)
	matcher := &fakeMatcher{matchNameToCompetitor: map[string]string{"Bravo Klinik": "Bravo Clinic"}}
	acts := &Operations{Store: analysisStore, Matcher: matcher}

	in := ReconcileEntitiesInput{
		AccountID:  accountID,
		BusinessID: businessID,
		RunID:      runID,
		Results: []ResultEntities{{
			ResultID: resultID,
			// Cite orders cover every case the link has: the business's own source,
			// two businesses sharing one directory citation, and a business the
			// answer named without citing anything for it.
			Entities: []llm.EntityWithCitations{
				{Entity: llm.ExtractedEntity{VerbatimName: "Atlas Dental", IsTarget: true, Excerpt: "Atlas Dental is great."}, CiteOrders: []int{0, 1}},
				{Entity: llm.ExtractedEntity{VerbatimName: "Bravo Clinic", Excerpt: "Bravo Clinic is nearby."}, CiteOrders: []int{1}},
				{Entity: llm.ExtractedEntity{VerbatimName: "Bravo Klinik", Excerpt: "Bravo Klinik also listed."}, CiteOrders: []int{}},
				{Entity: llm.ExtractedEntity{VerbatimName: "Charlie Medical", Excerpt: "Charlie Medical rounds it out."}, CiteOrders: []int{1}},
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

	// Links are many-to-many: Atlas has two sources, the directory supports two
	// competitors, and the unlinked variant stays unattributed.
	for _, want := range []struct {
		verbatim string
		count    int
	}{
		{"Atlas Dental", 2},
		{"Bravo Clinic", 1},
		{"Bravo Klinik", 0},
		{"Charlie Medical", 1},
	} {
		var got int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM mention_citations mc JOIN mentions m ON m.id=mc.mention_id
			WHERE m.prompt_result_id=$1 AND m.verbatim_name=$2`, resultID, want.verbatim).Scan(&got); err != nil {
			t.Fatalf("read mention citation (%s): %v", want.verbatim, err)
		}
		if got != want.count {
			t.Errorf("%s citation links = %d, want %d", want.verbatim, got, want.count)
		}
	}

	// Citation-gap consumes the persisted links through LoadMonitoringSnapshot.
	// The directory citation gets only its linked competitors; the unlinked
	// Bravo Klinik mention cannot create lift merely by sharing the answer.
	snapshot, err := analysisStore.LoadMonitoringSnapshot(ctx, accountID, businessID)
	if err != nil {
		t.Fatalf("LoadMonitoringSnapshot: %v", err)
	}
	if len(snapshot.Runs) != 1 || len(snapshot.Runs[0].Results) != 1 {
		t.Fatalf("snapshot shape = %+v", snapshot.Runs)
	}
	gotCitations := snapshot.Runs[0].Results[0].Citations
	if len(gotCitations) != 2 {
		t.Fatalf("snapshot citations = %+v", gotCitations)
	}
	if got := gotCitations[0].Competitors; len(got) != 0 {
		t.Errorf("self-only citation competitor lift = %v, want none", got)
	}
	if got := gotCitations[1].Competitors; len(got) != 2 || got[0] != "Bravo Clinic" || got[1] != "Charlie Medical" {
		t.Errorf("directory citation competitor lift = %v, want linked competitors only", got)
	}
	if got := gotCitations[1].Passage; got != "Bravo Clinic and Charlie Medical are recommended." {
		t.Errorf("directory citation passage = %q, want exact stored citation context", got)
	}

	// The discovered competitor exists with the verbatim name as its sole alias.
	var charlieStatus, charlieSource string
	if err := db.QueryRow(ctx, "SELECT status, source FROM competitors WHERE business_id = $1 AND name = 'Charlie Medical'", businessID).Scan(&charlieStatus, &charlieSource); err != nil {
		t.Fatalf("read discovered competitor: %v", err)
	}
	if charlieStatus != "discovered" || charlieSource != "discovered" {
		t.Errorf("discovered competitor = (%q, %q), want (discovered, discovered)", charlieStatus, charlieSource)
	}
	charlieAliases := textArray(t, db, ctx, "SELECT to_jsonb(aliases) FROM competitors WHERE business_id = $1 AND name = 'Charlie Medical'", businessID)
	if len(charlieAliases) != 1 || charlieAliases[0] != "Charlie Medical" {
		t.Errorf("discovered aliases = %v, want [Charlie Medical]", charlieAliases)
	}

	// The LLM variant landed in suggested_aliases, never promoted to aliases.
	bravoSuggested := textArray(t, db, ctx, "SELECT to_jsonb(suggested_aliases) FROM competitors WHERE id = $1", bravoID)
	bravoAliases := textArray(t, db, ctx, "SELECT to_jsonb(aliases) FROM competitors WHERE id = $1", bravoID)
	if countOccurrences(bravoSuggested, "Bravo Klinik") == 0 {
		t.Errorf("bravo suggested_aliases = %v, want to contain Bravo Klinik", bravoSuggested)
	}
	if countOccurrences(bravoAliases, "Bravo Klinik") != 0 {
		t.Errorf("bravo aliases = %v, must NOT contain the unapproved variant", bravoAliases)
	}

	// Commit stamped analysis_completed_at.
	var analysisCompleted *time.Time
	if err := db.QueryRow(ctx, "SELECT analysis_completed_at FROM monitoring_runs WHERE id = $1", runID).Scan(&analysisCompleted); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if analysisCompleted == nil {
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
	if got := countRows(t, db, ctx, "SELECT count(*) FROM competitors WHERE business_id = $1 AND name = 'Charlie Medical'", businessID); got != 1 {
		t.Errorf("Charlie competitor rows after rerun = %d, want 1 (no duplicate)", got)
	}
	if got := countRows(t, db, ctx, "SELECT count(*) FROM mentions WHERE prompt_result_id = $1", resultID); got != 4 {
		t.Errorf("mentions after rerun = %d, want 4 (delete-and-rewrite)", got)
	}
	bravoSuggested = textArray(t, db, ctx, "SELECT to_jsonb(suggested_aliases) FROM competitors WHERE id = $1", bravoID)
	if n := countOccurrences(bravoSuggested, "Bravo Klinik"); n != 1 {
		t.Errorf("Bravo Klinik appears %d times in suggested_aliases, want 1 (append idempotent)", n)
	}

	// A missing citation occurrence aborts the entire reconcile transaction:
	// the previous mentions and links survive and the run is never half-rewritten.
	err = analysisStore.CommitReconcile(ctx, accountID, businessID, store.ReconcileCommitParams{
		RunID: runID,
		Mentions: []store.MentionWrite{{
			PromptResultID: resultID, Subject: "self", MatchedBy: "exact", MentionOrder: 0,
			VerbatimName: "Atlas Dental", Excerpt: "Atlas Dental is great.", CiteOrders: []int{99},
		}},
	})
	if err == nil {
		t.Fatal("CommitReconcile with a missing citation order succeeded")
	}
	if got := countRows(t, db, ctx, "SELECT count(*) FROM mentions WHERE prompt_result_id=$1", resultID); got != 4 {
		t.Errorf("mentions after rolled-back reconcile = %d, want 4", got)
	}
	if got := countRows(t, db, ctx, `SELECT count(*) FROM mention_citations mc JOIN mentions m ON m.id=mc.mention_id WHERE m.prompt_result_id=$1`, resultID); got != 4 {
		t.Errorf("links after rolled-back reconcile = %d, want 4", got)
	}
}

func assertMention(t *testing.T, db *pgxpool.Pool, ctx context.Context, resultID domain.ID, subject string, order int, wantMatchedBy, wantVerbatim string) {
	t.Helper()
	var matchedBy, verbatimName string
	if err := db.QueryRow(ctx, `
		SELECT matched_by, verbatim_name FROM mentions WHERE prompt_result_id = $1 AND subject = $2 AND mention_order = $3`, resultID, subject, order).Scan(&matchedBy, &verbatimName); err != nil {
		t.Fatalf("read mention (subject=%s order=%d): %v", subject, order, err)
	}
	if matchedBy != wantMatchedBy {
		t.Errorf("mention (subject=%s order=%d) matched_by = %q, want %q", subject, order, matchedBy, wantMatchedBy)
	}
	if verbatimName != wantVerbatim {
		t.Errorf("mention (subject=%s order=%d) verbatim_name = %q, want %q", subject, order, verbatimName, wantVerbatim)
	}
}

func countRows(t *testing.T, db *pgxpool.Pool, ctx context.Context, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// textArray reads a single to_jsonb(text[]) column into []string.
func textArray(t *testing.T, db *pgxpool.Pool, ctx context.Context, query string, args ...any) []string {
	t.Helper()
	var raw []byte
	if err := db.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
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
