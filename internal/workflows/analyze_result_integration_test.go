package workflows

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/testsuite"
	testdb "opensight/internal/store/testdb"
)

// fakeExtractor returns a fixed extraction payload and counts invocations, so
// the integration test can prove AnalyzeResult always re-extracts (never
// short-circuits on an existing row) and retries a failing output exactly once.
type fakeExtractor struct {
	rawJSON json.RawMessage
	calls   int
}

func (f *fakeExtractor) RunExtraction(context.Context, llm.ExtractionInput) (llm.ExtractionRunResult, error) {
	f.calls++
	return llm.ExtractionRunResult{RawJSON: f.rawJSON, Model: "fake-mini"}, nil
}

const (
	analyzeResponseText = "Atlas Dental is a great clinic for braces."
	analyzeRawResponse  = `{"output":[{"type":"message","content":[{"type":"output_text",` +
		`"text":"Atlas Dental is a great clinic for braces.",` +
		`"annotations":[{"type":"url_citation","url":"https://ATLAS.example.com/team?utm_source=openai",` +
		`"title":"Atlas team","start_index":0,"end_index":12}]}]}]}`
	validExtraction = `{"entities":[{"verbatim_name":"Atlas Dental","is_target":true,` +
		`"excerpt":"Atlas Dental is a great clinic for braces."}],` +
		`"target":{"sentiment":"positive","keywords":["braces"],` +
		`"excerpts":["Atlas Dental is a great clinic for braces."]},` +
		`"citations":[{"url":"https://ATLAS.example.com/team?utm_source=openai","subject":"business"}]}`
	invalidExtraction = `{"entities":[{"verbatim_name":"Ghost Clinic","is_target":false,` +
		`"excerpt":"Ghost Clinic is cheapest."}],"target":null,"citations":[]}`
)

func TestAnalyzeResultAgainstPostgres(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run activity integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	tenantID := mustID(t)
	businessID := mustID(t)
	promptID := mustID(t)
	runID := mustID(t)
	resultID := mustID(t)

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query236, resultID)
		_, _ = testdb.Exec(ctx, db, testdb.Query237, resultID)
		_, _ = testdb.Exec(ctx, db, testdb.Query238, resultID)
		_, _ = testdb.Exec(ctx, db, testdb.Query239, runID)
		_, _ = testdb.Exec(ctx, db, testdb.Query240, promptID)
		_, _ = testdb.Exec(ctx, db, testdb.Query241, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query242, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query243, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Analyze Tenant")
	mustExec(t, db, ctx, testdb.Query244, businessID, tenantID)
	mustExec(t, db, ctx, testdb.Query245, promptID, businessID)
	mustExec(t, db, ctx, testdb.Query246, runID, businessID)
	mustExec(t, db, ctx, testdb.Query247, resultID, runID, promptID, analyzeRawResponse, analyzeResponseText)

	pool := db
	businesses := store.NewBusinessStore(pool)
	prompts := store.NewPromptStore(pool)
	runs := store.NewRunStore(pool)
	results := store.NewResultStore(pool)
	analysis := store.NewAnalysisStore(pool)

	t.Run("writes analysis and citations, always re-extracts", func(t *testing.T) {
		extractor := &fakeExtractor{rawJSON: json.RawMessage(validExtraction)}
		acts := &Activities{Businesses: businesses, Prompts: prompts, Runs: runs, Results: results, Analysis: analysis, Extractor: extractor}

		out, err := acts.AnalyzeResult(ctx, AnalyzeResultInput{TenantID: tenantID, ResultID: resultID})
		if err != nil {
			t.Fatalf("AnalyzeResult: %v", err)
		}
		if !out.Analyzed {
			t.Fatal("Analyzed = false, want true")
		}
		if len(out.Entities) != 1 || out.Entities[0].VerbatimName != "Atlas Dental" {
			t.Fatalf("entities = %+v, want the single Atlas Dental entity", out.Entities)
		}

		var sentiment string
		var model string
		var version int
		if err := testdb.QueryRow(ctx, db, testdb.Query248, resultID).Scan(&sentiment, &model, &version); err != nil {
			t.Fatalf("read result_analyses: %v", err)
		}
		if sentiment != "positive" || model != "fake-mini" || version != llm.ExtractionPromptVersion {
			t.Fatalf("analysis row = (%q, %q, %d)", sentiment, model, version)
		}

		var citeURL, citeDomain, citeSubject string
		var citeOrder int
		if err := testdb.QueryRow(ctx, db, testdb.Query249, resultID).Scan(&citeURL, &citeDomain, &citeSubject, &citeOrder); err != nil {
			t.Fatalf("read citations: %v", err)
		}
		// utm stripped, host lowercased, domain grouped.
		if citeURL != "https://atlas.example.com/team" {
			t.Errorf("citation url = %q, want normalized (utm stripped, host lowercased)", citeURL)
		}
		if citeDomain != "atlas.example.com" {
			t.Errorf("citation domain = %q", citeDomain)
		}
		if citeSubject != "business" || citeOrder != 0 {
			t.Errorf("citation subject/order = (%q, %d)", citeSubject, citeOrder)
		}

		// A second call must re-invoke the extractor (no short-circuit) and
		// overwrite rather than duplicate.
		if _, err := acts.AnalyzeResult(ctx, AnalyzeResultInput{TenantID: tenantID, ResultID: resultID}); err != nil {
			t.Fatalf("second AnalyzeResult: %v", err)
		}
		if extractor.calls != 2 {
			t.Fatalf("extractor calls = %d, want 2 (never short-circuits)", extractor.calls)
		}
		if got := citationCount(t, db, ctx, resultID); got != 1 {
			t.Fatalf("citations after re-analyze = %d, want 1 (overwrite, not duplicate)", got)
		}
	})

	t.Run("cross-tenant result is ErrNotFound", func(t *testing.T) {
		extractor := &fakeExtractor{rawJSON: json.RawMessage(validExtraction)}
		acts := &Activities{Businesses: businesses, Prompts: prompts, Runs: runs, Results: results, Analysis: analysis, Extractor: extractor}

		_, err := acts.AnalyzeResult(ctx, AnalyzeResultInput{TenantID: mustID(t), ResultID: resultID})
		if err == nil {
			t.Fatal("expected error for cross-tenant result")
		}
		if extractor.calls != 0 {
			t.Fatalf("extractor called %d times for a cross-tenant result, want 0", extractor.calls)
		}
	})

	t.Run("invalid output flags after MaxExtractionAttempts", func(t *testing.T) {
		// Wipe the row written by the success subtest so we can assert none exists.
		mustExec(t, db, ctx, testdb.Query250, resultID)
		mustExec(t, db, ctx, testdb.Query251, resultID)

		extractor := &fakeExtractor{rawJSON: json.RawMessage(invalidExtraction)}
		acts := &Activities{Businesses: businesses, Prompts: prompts, Runs: runs, Results: results, Analysis: analysis, Extractor: extractor}

		// The flagged path calls activity.GetLogger, so run it through a real
		// activity context (as the ExecutePrompt terminal-failure test does).
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()
		env.RegisterActivity(acts.AnalyzeResult)

		val, err := env.ExecuteActivity(acts.AnalyzeResult, AnalyzeResultInput{TenantID: tenantID, ResultID: resultID})
		if err != nil {
			t.Fatalf("AnalyzeResult returned error, want nil for a flagged result: %v", err)
		}
		var out AnalyzeResultOutput
		if err := val.Get(&out); err != nil {
			t.Fatalf("decode output: %v", err)
		}
		if out.Analyzed {
			t.Fatal("Analyzed = true, want false for a validation-failing output")
		}
		if extractor.calls != llm.MaxExtractionAttempts {
			t.Fatalf("extractor calls = %d, want %d", extractor.calls, llm.MaxExtractionAttempts)
		}
		var n int
		if err := testdb.QueryRow(ctx, db, testdb.Query252, resultID).Scan(&n); err != nil {
			t.Fatalf("count analyses: %v", err)
		}
		if n != 0 {
			t.Fatalf("result_analyses rows = %d, want 0 (flagged, not stored)", n)
		}
	})

	t.Run("reanalysis failure clears a prior successful analysis", func(t *testing.T) {
		// Seed a successful analysis first, as if from an earlier extraction_version.
		validExtractor := &fakeExtractor{rawJSON: json.RawMessage(validExtraction)}
		acts := &Activities{Businesses: businesses, Prompts: prompts, Runs: runs, Results: results, Analysis: analysis, Extractor: validExtractor}
		if _, err := acts.AnalyzeResult(ctx, AnalyzeResultInput{TenantID: tenantID, ResultID: resultID}); err != nil {
			t.Fatalf("seed AnalyzeResult: %v", err)
		}
		if got := citationCount(t, db, ctx, resultID); got != 1 {
			t.Fatalf("seeded citations = %d, want 1", got)
		}

		// A ReanalyzeRun-style re-run whose new extraction attempt fails must not
		// leave the prior row's stale sentiment/keywords counting toward metrics.
		invalidExtractor := &fakeExtractor{rawJSON: json.RawMessage(invalidExtraction)}
		acts = &Activities{Businesses: businesses, Prompts: prompts, Runs: runs, Results: results, Analysis: analysis, Extractor: invalidExtractor}
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()
		env.RegisterActivity(acts.AnalyzeResult)

		val, err := env.ExecuteActivity(acts.AnalyzeResult, AnalyzeResultInput{TenantID: tenantID, ResultID: resultID})
		if err != nil {
			t.Fatalf("AnalyzeResult returned error, want nil for a flagged reanalysis: %v", err)
		}
		var out AnalyzeResultOutput
		if err := val.Get(&out); err != nil {
			t.Fatalf("decode output: %v", err)
		}
		if out.Analyzed {
			t.Fatal("Analyzed = true, want false for a validation-failing reanalysis")
		}

		var n int
		if err := testdb.QueryRow(ctx, db, testdb.Query253, resultID).Scan(&n); err != nil {
			t.Fatalf("count analyses: %v", err)
		}
		if n != 0 {
			t.Fatalf("result_analyses rows = %d, want 0 (stale row cleared on reanalysis failure)", n)
		}
		if got := citationCount(t, db, ctx, resultID); got != 0 {
			t.Fatalf("citations after reanalysis failure = %d, want 0 (cleared with the analysis)", got)
		}
	})
}

func citationCount(t *testing.T, db *pgxpool.Pool, ctx context.Context, resultID domain.ID) int {
	t.Helper()
	var n int
	if err := testdb.QueryRow(ctx, db, testdb.Query254, resultID).Scan(&n); err != nil {
		t.Fatalf("count citations: %v", err)
	}
	return n
}
