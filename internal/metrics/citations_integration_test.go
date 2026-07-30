package metrics

import (
	"context"
	"os"
	"testing"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
)

func TestCitationSourcesGroupAndGate(t *testing.T) {
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
	otherBusinessID := mustNewID(t)
	p1, p2, p3, foreignPrompt := mustNewID(t), mustNewID(t), mustNewID(t), mustNewID(t)
	runA, runB := mustNewID(t), mustNewID(t)
	a1 := mustNewID(t) // analyzed, cites example.com twice
	a2 := mustNewID(t) // analyzed, cites example.com and other.com
	a3 := mustNewID(t) // succeeded but unanalyzed, citation must be excluded
	a4 := mustNewID(t) // malformed: run belongs to businessID, prompt belongs elsewhere
	b1 := mustNewID(t) // analyzed row exists but run is unreconciled, excluded

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query005, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query006, otherBusinessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query007, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query008, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Citation Tenant")
	mustExec(t, db, ctx, testdb.Query009, businessID, tenantID)
	mustExec(t, db, ctx, testdb.Query010, otherBusinessID, tenantID)
	mustExec(t, db, ctx, testdb.Query011, p1, businessID)
	mustExec(t, db, ctx, testdb.Query012, p2, businessID)
	mustExec(t, db, ctx, testdb.Query013, p3, businessID)
	mustExec(t, db, ctx, testdb.Query014, foreignPrompt, otherBusinessID)
	mustExec(t, db, ctx, testdb.Query015, runA, businessID)
	mustExec(t, db, ctx, testdb.Query016, runB, businessID)

	succeeded := func(id, runID, promptID domain.ID) {
		mustExec(t, db, ctx, testdb.Query017, id, runID, promptID)
	}
	succeeded(a1, runA, p1)
	succeeded(a2, runA, p2)
	succeeded(a3, runA, p3)
	succeeded(a4, runA, foreignPrompt)
	succeeded(b1, runB, p3)

	analysis := func(resultID domain.ID) {
		mustExec(t, db, ctx, testdb.Query018, resultID)
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
		mustExec(t, db, ctx, testdb.Query019, mustNewID(t), resultID, url, domainName, titleArg, subject, order)
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
