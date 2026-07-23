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
	"go.temporal.io/sdk/testsuite"
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
	slug := "analyze-" + planID.String()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM citations WHERE prompt_result_id = $1", resultID)
		_, _ = db.ExecContext(ctx, "DELETE FROM result_analyses WHERE prompt_result_id = $1", resultID)
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
		`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Analyze Tenant', $2)`, tenantID, planID)
	mustExec(t, db, ctx,
		`INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Atlas Dental', 'clinic', '{"country":"SG","city":"Singapore"}'::jsonb, now())`,
		businessID, tenantID)
	mustExec(t, db, ctx,
		`INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic for braces', 'active')`,
		promptID, businessID)
	mustExec(t, db, ctx,
		`INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'analyze-wf', now())`, runID, businessID)
	mustExec(t, db, ctx,
		`INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5', '{"model":"gpt-5"}'::jsonb, $4::jsonb, $5)`,
		resultID, runID, promptID, analyzeRawResponse, analyzeResponseText)

	businesses := store.NewBusinessStore(db)
	prompts := store.NewPromptStore(db)
	runs := store.NewRunStore(db)
	results := store.NewResultStore(db)
	analysis := store.NewAnalysisStore(db)

	t.Run("writes analysis and citations, always re-extracts", func(t *testing.T) {
		extractor := &fakeExtractor{rawJSON: json.RawMessage(validExtraction)}
		acts := NewActivities(businesses, prompts, runs, results, nil, analysis, extractor, nil, nil)

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
		if err := db.QueryRowContext(ctx,
			"SELECT sentiment, analysis_model, extraction_version FROM result_analyses WHERE prompt_result_id = $1",
			resultID).Scan(&sentiment, &model, &version); err != nil {
			t.Fatalf("read result_analyses: %v", err)
		}
		if sentiment != "positive" || model != "fake-mini" || version != llm.ExtractionPromptVersion {
			t.Fatalf("analysis row = (%q, %q, %d)", sentiment, model, version)
		}

		var citeURL, citeDomain, citeSubject string
		var citeOrder int
		if err := db.QueryRowContext(ctx,
			"SELECT url, domain, subject, cite_order FROM citations WHERE prompt_result_id = $1",
			resultID).Scan(&citeURL, &citeDomain, &citeSubject, &citeOrder); err != nil {
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
		acts := NewActivities(businesses, prompts, runs, results, nil, analysis, extractor, nil, nil)

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
		mustExec(t, db, ctx, "DELETE FROM citations WHERE prompt_result_id = $1", resultID)
		mustExec(t, db, ctx, "DELETE FROM result_analyses WHERE prompt_result_id = $1", resultID)

		extractor := &fakeExtractor{rawJSON: json.RawMessage(invalidExtraction)}
		acts := NewActivities(businesses, prompts, runs, results, nil, analysis, extractor, nil, nil)

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
		if err := db.QueryRowContext(ctx,
			"SELECT count(*) FROM result_analyses WHERE prompt_result_id = $1", resultID).Scan(&n); err != nil {
			t.Fatalf("count analyses: %v", err)
		}
		if n != 0 {
			t.Fatalf("result_analyses rows = %d, want 0 (flagged, not stored)", n)
		}
	})

	t.Run("reanalysis failure clears a prior successful analysis", func(t *testing.T) {
		// Seed a successful analysis first, as if from an earlier extraction_version.
		validExtractor := &fakeExtractor{rawJSON: json.RawMessage(validExtraction)}
		acts := NewActivities(businesses, prompts, runs, results, nil, analysis, validExtractor, nil, nil)
		if _, err := acts.AnalyzeResult(ctx, AnalyzeResultInput{TenantID: tenantID, ResultID: resultID}); err != nil {
			t.Fatalf("seed AnalyzeResult: %v", err)
		}
		if got := citationCount(t, db, ctx, resultID); got != 1 {
			t.Fatalf("seeded citations = %d, want 1", got)
		}

		// A ReanalyzeRun-style re-run whose new extraction attempt fails must not
		// leave the prior row's stale sentiment/keywords counting toward metrics.
		invalidExtractor := &fakeExtractor{rawJSON: json.RawMessage(invalidExtraction)}
		acts = NewActivities(businesses, prompts, runs, results, nil, analysis, invalidExtractor, nil, nil)
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
		if err := db.QueryRowContext(ctx,
			"SELECT count(*) FROM result_analyses WHERE prompt_result_id = $1", resultID).Scan(&n); err != nil {
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

func citationCount(t *testing.T, db *sql.DB, ctx context.Context, resultID domain.ID) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM citations WHERE prompt_result_id = $1", resultID).Scan(&n); err != nil {
		t.Fatalf("count citations: %v", err)
	}
	return n
}
