package metrics

import (
	"context"
	"os"
	"testing"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
)

// TestGatingRules is the one test suite the story asks for: it pins the metrics
// base gate against a seeded run so no future query can quietly widen it. The
// seed contains exactly the cases the gate must exclude — a succeeded-but-
// unanalyzed result (no result_analyses row), a failed result, and a whole run
// that is completed but not yet reconciled (analysis_completed_at NULL) — and
// asserts each is excluded from numerator and denominator alike, for both self
// visibility and competitor stats.
func TestGatingRules(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run metrics integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	tenantID := mustNewID(t)
	businessID := mustNewID(t)
	rivalID := mustNewID(t)
	manualRivalID := mustNewID(t)
	p1, p2, p3, p4 := mustNewID(t), mustNewID(t), mustNewID(t), mustNewID(t)
	runA, runB := mustNewID(t), mustNewID(t)
	// Run A results.
	a1 := mustNewID(t) // p1: analyzed, self + rival mention, positive, keyword+citation
	a2 := mustNewID(t) // p2: analyzed, rival mention only (no self), NULL sentiment
	a3 := mustNewID(t) // p3: succeeded but UNANALYZED (no result_analyses) — excluded
	a4 := mustNewID(t) // p4: failed — excluded
	// Run B result: run not reconciled (analysis_completed_at NULL) — excluded.
	b1 := mustNewID(t) // p1: has result_analyses + mentions, but run gate excludes it

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query020, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query021, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query022, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Metrics Tenant")
	mustExec(t, db, ctx, testdb.Query023, businessID, tenantID)
	for _, p := range []domain.ID{p1, p2, p3, p4} {
		mustExec(t, db, ctx, testdb.Query024, p, businessID)
	}
	mustExec(t, db, ctx, testdb.Query025, rivalID, businessID)
	mustExec(t, db, ctx, testdb.Query026, manualRivalID, businessID)

	// Run A: completed AND reconciled — the only run in the metrics base.
	mustExec(t, db, ctx, testdb.Query027, runA, businessID)
	// Run B: completed but analysis_completed_at NULL — must be excluded whole.
	mustExec(t, db, ctx, testdb.Query028, runB, businessID)

	succeeded := func(id, runID, promptID domain.ID, at string) {
		mustExec(t, db, ctx, testdb.Query029, id, runID, promptID, at)
	}
	succeeded(a1, runA, p1, "2026-07-06T00:00:00Z")
	succeeded(a2, runA, p2, "2026-07-06T00:00:00Z")
	succeeded(a3, runA, p3, "2026-07-06T00:00:00Z")
	mustExec(t, db, ctx, testdb.Query030, a4, runA, p4)
	succeeded(b1, runB, p1, "2026-07-13T00:00:00Z") // later week; excluded by run gate

	// result_analyses rows: a1, a2 (in base), and b1 (present but its run is not
	// reconciled, so the run gate must still drop it). a3 deliberately has none.
	analysis := func(id domain.ID, sentiment, keyword string) {
		var s any
		if sentiment == "" {
			s = nil
		} else {
			s = sentiment
		}
		mustExec(t, db, ctx, testdb.Query031, id, s, keyword)
	}
	analysis(a1, "positive", "friendly")
	analysis(a2, "", "expensive")
	analysis(b1, "positive", "excluded_kw")

	// citations: a1 -> example.com, a2 -> other.com, b1 -> excluded.com (run gate).
	citation := func(id, resultID domain.ID, domainName string) {
		mustExec(t, db, ctx, testdb.Query032, mustNewID(t), resultID, domainName)
	}
	citation(mustNewID(t), a1, "example.com")
	citation(mustNewID(t), a2, "other.com")
	citation(mustNewID(t), b1, "excluded.com")

	// mentions (canonical). a3 gets a self mention despite having no analysis row,
	// to prove the result_analyses gate — not the mention presence — decides.
	selfM := func(resultID domain.ID, order int) {
		mustExec(t, db, ctx, testdb.Query033, mustNewID(t), resultID, order)
	}
	rivalM := func(resultID domain.ID, order int) {
		mustExec(t, db, ctx, testdb.Query034, mustNewID(t), resultID, rivalID, order)
	}
	selfM(a1, 0)
	rivalM(a1, 1)
	rivalM(a2, 0)
	selfM(a3, 0)  // unanalyzed: must NOT count as a self mention
	selfM(b1, 0)  // run not reconciled: must NOT count
	rivalM(b1, 1) // ditto for the competitor

	m := New(db)

	// --- Visibility: one point (Run A), 1 self of 2 analyzed = 50%. ---
	trend, err := m.VisibilityTrend(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("VisibilityTrend: %v", err)
	}
	if len(trend) != 1 {
		t.Fatalf("visibility points = %d, want 1 (Run B excluded by run gate)", len(trend))
	}
	pt := trend[0]
	if pt.RunID != runA {
		t.Fatalf("visibility run = %v, want Run A", pt.RunID)
	}
	if pt.Analyzed != 2 || pt.Mentioned != 1 || pt.Percent != 50 {
		t.Fatalf("visibility = %d/%d (%.0f%%), want 1/2 (50%%)", pt.Mentioned, pt.Analyzed, pt.Percent)
	}
	assertIDSet(t, "visibility result_ids", pt.ResultIDs, a1, a2)

	// --- Competitor: Rival in both analyzed results = 100%, vs self +50. ---
	comp, err := m.CompetitorStats(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("CompetitorStats: %v", err)
	}
	if comp.TotalAnalyzed != 2 || comp.SelfMentioned != 1 || comp.SelfPercent != 50 {
		t.Fatalf("self base = %d/%d (%.0f%%), want 1/2 (50%%)", comp.SelfMentioned, comp.TotalAnalyzed, comp.SelfPercent)
	}
	assertIDSet(t, "self baseline result_ids", comp.ResultIDs, a1, a2)
	if len(comp.Competitors) != 2 {
		t.Fatalf("competitors = %d, want 2", len(comp.Competitors))
	}
	c := comp.Competitors[0]
	if c.Mentioned != 2 || c.TotalMentions != 2 || c.MentionPercent != 100 {
		t.Fatalf("rival = %d results / %d mentions / %.0f%%, want 2/2/100", c.Mentioned, c.TotalMentions, c.MentionPercent)
	}
	if c.AvgOrder != 0.5 {
		t.Fatalf("rival avg order = %v, want 0.5", c.AvgOrder)
	}
	if len(c.Aliases) != 1 || c.Aliases[0] != "rival clinic" ||
		len(c.SuggestedAliases) != 1 || c.SuggestedAliases[0] != "Rival Medical" {
		t.Fatalf("rival aliases = %#v suggested = %#v", c.Aliases, c.SuggestedAliases)
	}
	if c.VsSelf != 50 {
		t.Fatalf("rival vs self = %v, want 50", c.VsSelf)
	}
	assertIDSet(t, "rival result_ids", c.ResultIDs, a1, a2)
	if len(c.PerPrompt) != 2 {
		t.Fatalf("rival per-prompt appearances = %d, want 2 (p1, p2)", len(c.PerPrompt))
	}
	for _, p := range c.PerPrompt {
		if p.Text != "q" {
			t.Fatalf("rival prompt text = %q, want q", p.Text)
		}
	}
	if len(c.Trend) != 1 || c.Trend[0].RunID != runA || c.Trend[0].Percent != 100 {
		t.Fatalf("rival trend = %+v, want one Run A point at 100%%", c.Trend)
	}
	manual := comp.Competitors[1]
	if manual.CompetitorID != manualRivalID || manual.Mentioned != 0 || manual.TotalMentions != 0 ||
		manual.MentionPercent != 0 || manual.AvgOrder != 0 || len(manual.ResultIDs) != 0 ||
		len(manual.PerPrompt) != 0 {
		t.Fatalf("zero-history manual competitor = %+v", manual)
	}
	if len(manual.Trend) != 1 || manual.Trend[0].RunID != runA ||
		manual.Trend[0].Analyzed != 2 || manual.Trend[0].Mentioned != 0 || manual.Trend[0].Percent != 0 {
		t.Fatalf("manual competitor zero trend = %+v", manual.Trend)
	}

	// --- Aggregates: exclude unanalyzed (a3) and the un-reconciled run (b1). ---
	keywords, err := m.KeywordStats(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("KeywordStats: %v", err)
	}
	kw := map[string][]domain.ID{}
	for _, k := range keywords {
		kw[k.Keyword] = k.ResultIDs
	}
	if _, leaked := kw["excluded_kw"]; leaked {
		t.Fatal("excluded_kw present; b1's run should be gated out")
	}
	assertIDSet(t, "keyword friendly", kw["friendly"], a1)
	assertIDSet(t, "keyword expensive", kw["expensive"], a2)

	domains, err := m.CitationDomainStats(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("CitationDomainStats: %v", err)
	}
	dom := map[string][]domain.ID{}
	for _, d := range domains {
		dom[d.Domain] = d.ResultIDs
	}
	if _, leaked := dom["excluded.com"]; leaked {
		t.Fatal("excluded.com present; b1's run should be gated out")
	}
	assertIDSet(t, "domain example.com", dom["example.com"], a1)

	sentiments, err := m.SentimentStats(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("SentimentStats: %v", err)
	}
	if len(sentiments) != 1 || sentiments[0].Sentiment != "positive" {
		t.Fatalf("sentiments = %+v, want one positive (a2 NULL, b1 gated)", sentiments)
	}
	assertIDSet(t, "sentiment positive", sentiments[0].ResultIDs, a1)

	// --- Prompt presence: p1 (mentioned, order 0), p2 (absent mention);
	// p3 has only an unanalyzed result and p4 only a failed one, so both are
	// absent from the analyzed base entirely. p1's latest stays a1, not b1. ---
	latest, err := m.PromptLatestStats(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("PromptLatestStats: %v", err)
	}
	byPrompt := map[domain.ID]PromptLatest{}
	for _, l := range latest {
		byPrompt[l.PromptID] = l
	}
	if len(latest) != 2 {
		t.Fatalf("prompt latest = %d, want 2 (p1, p2)", len(latest))
	}
	if l := byPrompt[p1]; l.ResultID != a1 || !l.Mentioned || l.MentionOrder == nil || *l.MentionOrder != 0 {
		t.Fatalf("p1 latest = %+v, want a1 mentioned at order 0", l)
	}
	if l := byPrompt[p2]; l.ResultID != a2 || l.Mentioned {
		t.Fatalf("p2 latest = %+v, want a2 not mentioned", l)
	}

	// --- Prompt-set-change classification (POL-2 trend markers): the four prompts
	// were created together (an add on their creation day). Then, on a later day,
	// replace p2 (retire p2 + insert p5 as its successor) and add a fresh p6 with
	// no predecessor. That later day must classify as 1 replaced + 1 added, with
	// p2's retirement absorbed into the replace, not double-counted as an outright
	// retire. This is the real-DB check of the per-day (added, retired, replaced)
	// query. ---
	p5, p6 := mustNewID(t), mustNewID(t)
	mustExec(t, db, ctx, testdb.Query035, p2)
	mustExec(t, db, ctx, testdb.Query036, p5, businessID, p2)
	mustExec(t, db, ctx, testdb.Query037, p6, businessID)

	changes, err := m.PromptChanges(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("PromptChanges: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("prompt changes = %+v, want 2 distinct days", changes)
	}
	if c := changes[0]; c.Added != 4 || c.Retired != 0 || c.Replaced != 0 {
		t.Fatalf("day 1 change = %+v, want 4 added", c)
	}
	if c := changes[1]; c.Added != 1 || c.Retired != 0 || c.Replaced != 1 {
		t.Fatalf("day 2 change = %+v, want 1 added + 1 replaced (p2 retire absorbed)", c)
	}
}

// TestCompetitorTrendIncludesZeroMentionRuns pins the chart contract for
// competitors: a weekly trend includes analyzed weeks where a competitor was not
// mentioned, so the UI can show a real drop to 0% instead of connecting only the
// weeks where the competitor appeared.
func TestCompetitorTrendIncludesZeroMentionRuns(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run metrics integration tests")
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
	competitorID := mustNewID(t)
	runA, runB := mustNewID(t), mustNewID(t)
	resultA, resultB := mustNewID(t), mustNewID(t)

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query038, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query039, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query040, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Competitor Trend Tenant")
	mustExec(t, db, ctx, testdb.Query041, businessID, tenantID)
	mustExec(t, db, ctx, testdb.Query042, promptID, businessID)
	mustExec(t, db, ctx, testdb.Query043, competitorID, businessID)
	mustExec(t, db, ctx, testdb.Query044, runA, businessID)
	mustExec(t, db, ctx, testdb.Query045, runB, businessID)

	succeeded := func(id, runID domain.ID, at string) {
		mustExec(t, db, ctx, testdb.Query046, id, runID, promptID, at)
		mustExec(t, db, ctx, testdb.Query047, id)
	}
	succeeded(resultA, runA, "2026-07-06T00:00:00Z")
	succeeded(resultB, runB, "2026-07-13T00:00:00Z")
	mustExec(t, db, ctx, testdb.Query048, mustNewID(t), resultA, competitorID)

	stats, err := New(db).CompetitorStats(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("CompetitorStats: %v", err)
	}
	if len(stats.Competitors) != 1 {
		t.Fatalf("competitors = %d, want 1", len(stats.Competitors))
	}
	trend := stats.Competitors[0].Trend
	if len(trend) != 2 {
		t.Fatalf("trend points = %d, want 2: %+v", len(trend), trend)
	}
	if trend[0].RunID != runA || trend[0].Mentioned != 1 || trend[0].Analyzed != 1 || trend[0].Percent != 100 {
		t.Fatalf("first trend point = %+v, want Run A at 100%%", trend[0])
	}
	assertIDSet(t, "first trend result_ids", trend[0].ResultIDs, resultA)
	if trend[1].RunID != runB || trend[1].Mentioned != 0 || trend[1].Analyzed != 1 || trend[1].Percent != 0 || len(trend[1].ResultIDs) != 0 {
		t.Fatalf("second trend point = %+v, want Run B at 0%% with no result_ids", trend[1])
	}
}

func assertIDSet(t *testing.T, label string, got []domain.ID, want ...domain.ID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	set := map[domain.ID]bool{}
	for _, id := range got {
		set[id] = true
	}
	for _, id := range want {
		if !set[id] {
			t.Fatalf("%s = %v, missing %v", label, got, id)
		}
	}
}

func mustNewID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	return id
}

func mustExec(t *testing.T, db *pgxpool.Pool, ctx context.Context, query testdb.Query, args ...any) {
	t.Helper()
	if _, err := testdb.Exec(ctx, db, query, args...); err != nil {
		t.Fatalf("exec test query %d: %v", query, err)
	}
}
