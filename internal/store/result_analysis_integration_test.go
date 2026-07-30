package store

import (
	"context"
	"os"
	"testing"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
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

func seedResultAnalysisBusiness(t *testing.T, db *pgxpool.Pool, ctx context.Context) resultAnalysisFixture {
	t.Helper()

	fx := resultAnalysisFixture{
		tenantID:   mustNewID(t),
		businessID: mustNewID(t),
		promptID:   mustNewID(t),
		promptID2:  mustNewID(t),
		runID:      mustNewID(t),
	}

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query286, fx.businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query287, fx.businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query288, fx.businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query149, fx.businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query150, fx.businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query151, fx.businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query152, fx.businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query153, fx.businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query154, fx.tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query155, fx.tenantID)
	})

	insertTenant(t, db, ctx, fx.tenantID, "Result Analysis Tenant")
	mustExec(t, db, ctx, testdb.Query156, fx.businessID, fx.tenantID)
	mustExec(t, db, ctx, testdb.Query157, fx.promptID, fx.businessID)
	mustExec(t, db, ctx, testdb.Query158, fx.promptID2, fx.businessID)
	mustExec(t, db, ctx, testdb.Query159, fx.runID, fx.businessID)

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
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	fx := seedResultAnalysisBusiness(t, db, ctx)
	analyzedID := mustNewID(t)
	unanalyzedID := mustNewID(t)
	competitorID := mustNewID(t)
	selfMentionID := mustNewID(t)
	competitorMentionID := mustNewID(t)
	citationID := mustNewID(t)

	mustExec(t, db, ctx, testdb.Query160, analyzedID, fx.runID, fx.promptID)
	mustExec(t, db, ctx, testdb.Query161, unanalyzedID, fx.runID, fx.promptID2)

	mustExec(t, db, ctx, testdb.Query162, competitorID, fx.businessID)
	mustExec(t, db, ctx, testdb.Query163, analyzedID)
	mustExec(t, db, ctx, testdb.Query164, selfMentionID, analyzedID)
	mustExec(t, db, ctx, testdb.Query165, competitorMentionID, analyzedID, competitorID)
	mustExec(t, db, ctx, testdb.Query166, citationID, analyzedID)

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
	if got.Mentions[0].VerbatimName != "Atlas Clinic" {
		t.Fatalf("mention[0] verbatim_name = %q, want Atlas Clinic", got.Mentions[0].VerbatimName)
	}
	if got.Mentions[1].Subject != "competitor" || got.Mentions[1].MentionOrder != 1 {
		t.Fatalf("mention[1] = %+v, want competitor/1", got.Mentions[1])
	}
	if got.Mentions[1].VerbatimName != "Rival Clinic" {
		t.Fatalf("mention[1] verbatim_name = %q, want Rival Clinic", got.Mentions[1].VerbatimName)
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
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	fx := seedResultAnalysisBusiness(t, db, ctx)
	mentionedID := mustNewID(t)   // analyzed, has a self mention
	unmentionedID := mustNewID(t) // succeeded, no mention, no analysis
	selfMentionID := mustNewID(t)

	mustExec(t, db, ctx, testdb.Query167, mentionedID, fx.runID, fx.promptID)
	mustExec(t, db, ctx, testdb.Query168, unmentionedID, fx.runID, fx.promptID2)
	mustExec(t, db, ctx, testdb.Query169, mentionedID)
	mustExec(t, db, ctx, testdb.Query170, selfMentionID, mentionedID)

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
