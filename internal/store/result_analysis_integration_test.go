package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"opensight/internal/domain"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// resultAnalysisFixture is the plan/tenant/business/prompt/run scaffolding the
// MET-5 read-path tests share; each test inserts its own prompt_results on top.
type resultAnalysisFixture struct {
	tenantID   domain.ID
	businessID domain.ID
	promptID   domain.ID
	promptID2  domain.ID
	runID      domain.ID
}

func seedResultAnalysisBusiness(t *testing.T, db *sql.DB, ctx context.Context) resultAnalysisFixture {
	t.Helper()

	planID := mustNewID(t)
	fx := resultAnalysisFixture{
		tenantID:   mustNewID(t),
		businessID: mustNewID(t),
		promptID:   mustNewID(t),
		promptID2:  mustNewID(t),
		runID:      mustNewID(t),
	}
	slug := "result-analysis-" + planID.String()

	t.Cleanup(func() {
		child := `IN (SELECT pr.id FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id WHERE r.business_id = $1)`
		_, _ = db.ExecContext(ctx, "DELETE FROM mentions WHERE prompt_result_id "+child, fx.businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM citations WHERE prompt_result_id "+child, fx.businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM result_analyses WHERE prompt_result_id "+child, fx.businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM prompt_results WHERE run_id IN (SELECT id FROM monitoring_runs WHERE business_id = $1)", fx.businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM competitors WHERE business_id = $1", fx.businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM monitoring_runs WHERE business_id = $1", fx.businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM prompts WHERE business_id = $1", fx.businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", fx.businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", fx.tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM plans WHERE id = $1", planID)
	})

	mustExec(t, db, ctx,
		`INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ($1, $2, 5, 'test', ARRAY['chatgpt']::text[])`, planID, slug)
	mustExec(t, db, ctx,
		`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Result Analysis Tenant', $2)`, fx.tenantID, planID)
	mustExec(t, db, ctx,
		`INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Result Analysis Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`, fx.businessID, fx.tenantID)
	mustExec(t, db, ctx,
		`INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic near me', 'active')`, fx.promptID, fx.businessID)
	mustExec(t, db, ctx,
		`INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'cheapest clinic near me', 'active')`, fx.promptID2, fx.businessID)
	mustExec(t, db, ctx,
		`INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'result-analysis-workflow', now(), now())`, fx.runID, fx.businessID)

	return fx
}

// TestGetResultAnalysis pins the MET-5 drawer read model: an analyzed result
// returns its sentiment/keywords/excerpts plus mentions and citations, a
// succeeded-but-unanalyzed result returns Analyzed=false with empty slices, and
// the tenant scoping makes a foreign tenant look exactly like the unanalyzed
// case rather than leaking another tenant's analysis.
func TestGetResultAnalysis(t *testing.T) {
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

	fx := seedResultAnalysisBusiness(t, db, ctx)
	analyzedID := mustNewID(t)
	unanalyzedID := mustNewID(t)
	competitorID := mustNewID(t)
	selfMentionID := mustNewID(t)
	competitorMentionID := mustNewID(t)
	citationID := mustNewID(t)

	mustExec(t, db, ctx,
		`INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_1"}'::jsonb, 'Clinic and Rival are options.')`,
		analyzedID, fx.runID, fx.promptID)
	mustExec(t, db, ctx,
		`INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_2"}'::jsonb, 'No mention here.')`,
		unanalyzedID, fx.runID, fx.promptID2)

	mustExec(t, db, ctx,
		`INSERT INTO competitors (id, business_id, name, source, status)
VALUES ($1, $2, 'Rival Clinic', 'discovered', 'discovered')`, competitorID, fx.businessID)
	mustExec(t, db, ctx,
		`INSERT INTO result_analyses (prompt_result_id, sentiment, keywords, excerpts, analysis_model, extraction_version)
VALUES ($1, 'positive', ARRAY['friendly','affordable']::text[], '["Clinic is a good option."]'::jsonb, 'gpt-5-mini', 1)`, analyzedID)
	mustExec(t, db, ctx,
		`INSERT INTO mentions (id, prompt_result_id, subject, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'self', 'exact', 0, 'Clinic ... options.')`, selfMentionID, analyzedID)
	mustExec(t, db, ctx,
		`INSERT INTO mentions (id, prompt_result_id, subject, competitor_id, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'competitor', $3, 'llm', 1, 'Rival ... options.')`, competitorMentionID, analyzedID, competitorID)
	mustExec(t, db, ctx,
		`INSERT INTO citations (id, prompt_result_id, url, domain, title, cite_order, subject)
VALUES ($1, $2, 'https://example.com/x', 'example.com', 'Example', 0, 'business')`, citationID, analyzedID)

	store := NewResultStore(db)

	got, err := store.GetResultAnalysis(ctx, fx.tenantID, analyzedID)
	if err != nil {
		t.Fatalf("GetResultAnalysis(analyzed): %v", err)
	}
	if !got.Analyzed {
		t.Fatal("Analyzed = false, want true for a result with a result_analyses row")
	}
	if got.Sentiment == nil || *got.Sentiment != "positive" {
		t.Fatalf("sentiment = %v, want positive", got.Sentiment)
	}
	if len(got.Keywords) != 2 || got.Keywords[0] != "friendly" || got.Keywords[1] != "affordable" {
		t.Fatalf("keywords = %v, want [friendly affordable]", got.Keywords)
	}
	if len(got.Excerpts) != 1 || got.Excerpts[0] != "Clinic is a good option." {
		t.Fatalf("excerpts = %v, want one excerpt", got.Excerpts)
	}
	// Mentions come back in mention_order: self (0) then competitor (1).
	if len(got.Mentions) != 2 {
		t.Fatalf("mentions = %d, want 2", len(got.Mentions))
	}
	if got.Mentions[0].Subject != "self" || got.Mentions[0].MatchedBy != "exact" || got.Mentions[0].MentionOrder != 0 {
		t.Fatalf("mention[0] = %+v, want self/exact/0", got.Mentions[0])
	}
	if got.Mentions[1].Subject != "competitor" || got.Mentions[1].MentionOrder != 1 {
		t.Fatalf("mention[1] = %+v, want competitor/1", got.Mentions[1])
	}
	if len(got.Citations) != 1 || got.Citations[0].URL != "https://example.com/x" || got.Citations[0].Subject != "business" || got.Citations[0].CiteOrder != 0 {
		t.Fatalf("citations = %+v, want one business citation", got.Citations)
	}

	// Succeeded-but-unanalyzed: no result_analyses row, empty child slices.
	un, err := store.GetResultAnalysis(ctx, fx.tenantID, unanalyzedID)
	if err != nil {
		t.Fatalf("GetResultAnalysis(unanalyzed): %v", err)
	}
	if un.Analyzed || un.Sentiment != nil || len(un.Mentions) != 0 || len(un.Citations) != 0 {
		t.Fatalf("unanalyzed result = %+v, want Analyzed=false and empty children", un)
	}

	// A foreign tenant must not see the analyzed result's analysis: it looks
	// exactly like the unanalyzed case, not an error.
	cross, err := store.GetResultAnalysis(ctx, mustNewID(t), analyzedID)
	if err != nil {
		t.Fatalf("GetResultAnalysis(cross-tenant): %v", err)
	}
	if cross.Analyzed || len(cross.Mentions) != 0 || len(cross.Citations) != 0 {
		t.Fatalf("cross-tenant analysis = %+v, want empty/unanalyzed", cross)
	}
}

// TestListResultsMentionedFilterAndAnalyzedFlag pins MET-5's Responses-list
// additions: the mentioned filter keeps only results with a subject='self'
// mention, and ResultListItem.Analyzed reflects the presence of a
// result_analyses row.
func TestListResultsMentionedFilterAndAnalyzedFlag(t *testing.T) {
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

	fx := seedResultAnalysisBusiness(t, db, ctx)
	mentionedID := mustNewID(t)   // analyzed, has a self mention
	unmentionedID := mustNewID(t) // succeeded, no mention, no analysis
	selfMentionID := mustNewID(t)

	mustExec(t, db, ctx,
		`INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_1"}'::jsonb, 'Clinic is an option.')`,
		mentionedID, fx.runID, fx.promptID)
	mustExec(t, db, ctx,
		`INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_2"}'::jsonb, 'Nobody relevant.')`,
		unmentionedID, fx.runID, fx.promptID2)
	mustExec(t, db, ctx,
		`INSERT INTO result_analyses (prompt_result_id, sentiment, keywords, excerpts, analysis_model, extraction_version)
VALUES ($1, 'positive', '{}'::text[], '[]'::jsonb, 'gpt-5-mini', 1)`, mentionedID)
	mustExec(t, db, ctx,
		`INSERT INTO mentions (id, prompt_result_id, subject, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'self', 'exact', 0, 'Clinic ... option.')`, selfMentionID, mentionedID)

	store := NewResultStore(db)

	analyzedByID := func(items []ResultListItem) map[domain.ID]bool {
		m := make(map[domain.ID]bool, len(items))
		for _, it := range items {
			m[it.ID] = it.Analyzed
		}
		return m
	}

	// No filter: both results present, Analyzed flag set only on the analyzed one.
	all, err := store.ListResults(ctx, fx.tenantID, fx.businessID, ResultFilter{})
	if err != nil {
		t.Fatalf("ListResults(no filter): %v", err)
	}
	flags := analyzedByID(all)
	if len(all) != 2 {
		t.Fatalf("ListResults(no filter) = %d rows, want 2", len(all))
	}
	if !flags[mentionedID] {
		t.Fatal("analyzed result has Analyzed=false, want true")
	}
	if flags[unmentionedID] {
		t.Fatal("unanalyzed result has Analyzed=true, want false")
	}

	// mentioned=true keeps only the self-mention result.
	yes := true
	withMention, err := store.ListResults(ctx, fx.tenantID, fx.businessID, ResultFilter{Mentioned: &yes})
	if err != nil {
		t.Fatalf("ListResults(mentioned=true): %v", err)
	}
	if len(withMention) != 1 || withMention[0].ID != mentionedID {
		t.Fatalf("ListResults(mentioned=true) = %+v, want only the mentioned result", withMention)
	}

	// mentioned=false keeps only the non-self-mention result.
	no := false
	withoutMention, err := store.ListResults(ctx, fx.tenantID, fx.businessID, ResultFilter{Mentioned: &no})
	if err != nil {
		t.Fatalf("ListResults(mentioned=false): %v", err)
	}
	if len(withoutMention) != 1 || withoutMention[0].ID != unmentionedID {
		t.Fatalf("ListResults(mentioned=false) = %+v, want only the unmentioned result", withoutMention)
	}
}
