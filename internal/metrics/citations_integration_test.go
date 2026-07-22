package metrics

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"opensight/internal/domain"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestCitationSourcesGroupAndGate(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run metrics integration tests")
	}

	ctx := context.Background()
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	planID := mustNewID(t)
	tenantID := mustNewID(t)
	businessID := mustNewID(t)
	otherBusinessID := mustNewID(t)
	p1, p2, p3, foreignPrompt := mustNewID(t), mustNewID(t), mustNewID(t), mustNewID(t)
	runA, runB := mustNewID(t), mustNewID(t)
	a1 := mustNewID(t) // analyzed, cites example.com twice
	a2 := mustNewID(t) // analyzed, cites example.com and other.com
	a3 := mustNewID(t) // succeeded but unanalyzed, citation must be excluded
	a4 := mustNewID(t) // malformed: run belongs to businessID, prompt belongs elsewhere
	b1 := mustNewID(t) // analyzed row exists but run is unreconciled, excluded
	slug := "citation-sources-" + planID.String()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", otherBusinessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM plans WHERE id = $1", planID)
	})

	mustExec(t, db, ctx, `INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ($1, $2, 20, 'test', ARRAY['chatgpt']::text[])`, planID, slug)
	mustExec(t, db, ctx, `INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Citation Tenant', $2)`, tenantID, planID)
	mustExec(t, db, ctx, `INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Atlas Clinic')`, businessID, tenantID)
	mustExec(t, db, ctx, `INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Other Clinic')`, otherBusinessID, tenantID)
	mustExec(t, db, ctx, `INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'root canal clinic', 'active')`, p1, businessID)
	mustExec(t, db, ctx, `INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best specialist near me', 'active')`, p2, businessID)
	mustExec(t, db, ctx, `INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'excluded prompt', 'active')`, p3, businessID)
	mustExec(t, db, ctx, `INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'foreign prompt text', 'active')`, foreignPrompt, otherBusinessID)
	mustExec(t, db, ctx, `INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-06', 'completed', 'wf-cite-a', now(), now())`, runA, businessID)
	mustExec(t, db, ctx, `INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'wf-cite-b', now())`, runB, businessID)

	succeeded := func(id, runID, promptID domain.ID) {
		mustExec(t, db, ctx, `INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text, requested_at, completed_at)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{}'::jsonb, '{"id":"r"}'::jsonb, 'text', now(), now())`, id, runID, promptID)
	}
	succeeded(a1, runA, p1)
	succeeded(a2, runA, p2)
	succeeded(a3, runA, p3)
	succeeded(a4, runA, foreignPrompt)
	succeeded(b1, runB, p3)

	analysis := func(resultID domain.ID) {
		mustExec(t, db, ctx, `INSERT INTO result_analyses (prompt_result_id, keywords, excerpts, analysis_model, extraction_version)
VALUES ($1, ARRAY['useful']::text[], '[]'::jsonb, 'gpt-5-mini', 1)`, resultID)
	}
	analysis(a1)
	analysis(a2)
	analysis(a4)
	analysis(b1)

	citation := func(resultID domain.ID, url, title, domainName, subject string, order int) {
		var titleArg any
		if title == "" {
			titleArg = nil
		} else {
			titleArg = title
		}
		mustExec(t, db, ctx, `INSERT INTO citations (id, prompt_result_id, url, domain, title, subject, cite_order)
VALUES ($1, $2, $3, $4, $5, $6, $7)`, mustNewID(t), resultID, url, domainName, titleArg, subject, order)
	}
	// Duplicate annotations in a1 must still count as one source/page/business
	// frequency because frequency is distinct analyzed responses.
	citation(a1, "https://example.com/guide", "Guide", "example.com", "business", 0)
	citation(a1, "https://example.com/guide", "Guide", "example.com", "business", 1)
	citation(a2, "https://example.com/rivals", "", "example.com", "competitor", 0)
	citation(a2, "https://example.com/unknown", "", "example.com", "unknown", 1)
	citation(a2, "https://other.com/page", "Other", "other.com", "other", 2)
	citation(a3, "https://leak.com/page", "Leak", "leak.com", "business", 0)
	citation(a4, "https://foreign.com/page", "Foreign", "foreign.com", "business", 0)
	citation(b1, "https://excluded.com/page", "Excluded", "excluded.com", "other", 0)

	sources, err := New(db).CitationSources(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("CitationSources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %d, want 2: %+v", len(sources), sources)
	}
	if sources[0].Domain != "example.com" || sources[0].Frequency != 2 {
		t.Fatalf("first source = %+v, want example.com frequency 2", sources[0])
	}
	if sources[1].Domain != "other.com" || sources[1].Frequency != 1 {
		t.Fatalf("second source = %+v, want other.com frequency 1", sources[1])
	}
	assertIDSet(t, "example source result_ids", sources[0].ResultIDs, a1, a2)
	assertIDSet(t, "business subject ids", sources[0].Subjects.Business.ResultIDs, a1)
	assertIDSet(t, "competitor subject ids", sources[0].Subjects.Competitor.ResultIDs, a2)
	assertIDSet(t, "unknown subject ids", sources[0].Subjects.Unknown.ResultIDs, a2)
	if sources[0].Subjects.Business.Frequency != 1 || sources[0].Subjects.Competitor.Frequency != 1 || sources[0].Subjects.Unknown.Frequency != 1 {
		t.Fatalf("subject frequencies = %+v", sources[0].Subjects)
	}
	if len(sources[0].Pages) != 3 {
		t.Fatalf("example pages = %d, want 3: %+v", len(sources[0].Pages), sources[0].Pages)
	}
	guide := pageByURL(t, sources[0].Pages, "https://example.com/guide")
	if guide.Title == nil || *guide.Title != "Guide" || guide.Frequency != 1 {
		t.Fatalf("guide page = %+v, want title Guide frequency 1", guide)
	}
	assertIDSet(t, "guide page result_ids", guide.ResultIDs, a1)
	if len(sources[0].Prompts) != 2 {
		t.Fatalf("example prompts = %d, want 2: %+v", len(sources[0].Prompts), sources[0].Prompts)
	}
	for _, prompt := range sources[0].Prompts {
		if prompt.Frequency != 1 {
			t.Fatalf("prompt frequency = %+v, want 1", prompt)
		}
	}
	for _, source := range sources {
		if source.Domain == "leak.com" || source.Domain == "excluded.com" || source.Domain == "foreign.com" {
			t.Fatalf("gated source leaked: %+v", source)
		}
	}
}

func pageByURL(t *testing.T, pages []CitationPage, url string) CitationPage {
	t.Helper()
	for _, page := range pages {
		if page.URL == url {
			return page
		}
	}
	t.Fatalf("page %s not found in %+v", url, pages)
	return CitationPage{}
}
