package store

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/visibility"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestFindingLifecycleAcrossRuns covers the three rules that replaced numbered
// action cycles: a finding the evidence stops producing drops out of the queue,
// a completed one that comes back reopens, a completed one that stays gone is
// confirmed fixed — but only by a run that could actually check — and a
// dismissed one is never resurrected.
func TestFindingLifecycleAcrossRuns(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	s := New(db)
	accountID, businessID := mustNewID(t), mustNewID(t)
	insertAccount(t, db, ctx, accountID, "Improve Lifecycle Account")
	mustExec(t, db, ctx, "INSERT INTO businesses (id,account_id,status,name,category,location,activated_at) VALUES ($1,$2,'active','Lifecycle Clinic','clinic','{\"country\":\"SG\"}'::jsonb,now())", businessID, accountID)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id=$1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id=$1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id=$1", accountID)
	})

	const blockedKey = "site-audit:robots_allows_oai_searchbot"
	const sitemapKey = "site-audit:sitemap_published"
	const listingKey = "citation-gap:healthhub.sg"
	blocked := visibility.Finding{Key: blockedKey, Source: visibility.SourceSiteAudit, Category: visibility.GroupAccess, Title: "Allow OAI-SearchBot", Body: "b", Blocking: true}
	sitemap := visibility.Finding{Key: sitemapKey, Source: visibility.SourceSiteAudit, Category: visibility.GroupStructure, Title: "Publish a sitemap", Body: "b"}
	listing := visibility.Finding{Key: listingKey, Source: visibility.SourceCitationGap, Category: visibility.CategoryListings, Title: "Get listed on healthhub.sg", Body: "b", Reach: 9, Priority: 1}

	runNumber := 0
	publishRun := func(run ImproveRun) {
		t.Helper()
		runNumber++
		runID := mustNewID(t)
		scheduled := time.Date(2026, 1, 1+runNumber, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		mustExec(t, db, ctx, "INSERT INTO monitoring_runs (id,business_id,platform,trigger,scheduled_for,status,workflow_id,completed_at,analysis_completed_at) VALUES ($1,$2,'chatgpt','scheduled',$3,'completed',$4,now(),now())", runID, businessID, scheduled, "improve-"+runID.String())
		run.RunID = runID
		if err := s.PublishImproveRun(ctx, accountID, businessID, run); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(findings ...visibility.Finding) {
		t.Helper()
		publishRun(ImproveRun{PagesRead: 3, Findings: findings})
	}
	active := func() map[string]FindingRecord {
		t.Helper()
		rows, err := s.ListFindings(ctx, accountID, businessID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]FindingRecord{}
		for _, row := range rows {
			out[row.Key] = row
		}
		return out
	}
	find := func(key string) FindingRecord {
		t.Helper()
		row, ok := active()[key]
		if !ok {
			t.Fatalf("finding %s is not listed", key)
		}
		return row
	}
	setStatus := func(id domain.ID, status FindingStatus, reason *string) {
		t.Helper()
		if _, err := s.SetFindingStatus(ctx, accountID, id, status, reason); err != nil {
			t.Fatal(err)
		}
	}

	// Run 1 produces all three findings.
	publish(blocked, sitemap, listing)
	if got := len(active()); got != 3 {
		t.Fatalf("run 1 listed %d findings, want 3", got)
	}
	if got := find(sitemapKey).Category; got != visibility.GroupStructure {
		t.Errorf("category round-tripped as %q, want %q", got, visibility.GroupStructure)
	}
	// The queue's order is the product's triage, and the query is what the page
	// actually reads — so it is pinned here rather than only over the in-memory
	// ranking. The listing outranks both site findings on reach and still comes
	// last, because it is the work the business is least able to finish alone.
	rows, err := s.ListFindings(ctx, accountID, businessID)
	if err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		order = append(order, row.Key)
	}
	if want := []string{blockedKey, sitemapKey, listingKey}; !slices.Equal(order, want) {
		t.Errorf("queue order = %v, want %v", order, want)
	}

	// Run 2 no longer produces the sitemap finding, so it drops out of the queue
	// without any retirement bookkeeping.
	publish(blocked)
	if _, listed := active()[sitemapKey]; listed {
		t.Error("a finding the evidence stopped producing is still active")
	}

	// Completing then seeing it again reopens it: the only regression rule.
	setStatus(find(blockedKey).ID, FindingDone, nil)
	publish(blocked)
	reopened := find(blockedKey)
	if reopened.Status != FindingOpen {
		t.Errorf("status after recurrence = %s, want OPEN", reopened.Status)
	}
	if reopened.CompletedAt != nil {
		t.Error("a reopened finding kept its completion stamp")
	}

	// Completing and then not seeing it again leaves it done and reachable. The
	// product records nothing further: a later run not reproducing a finding is
	// not evidence that the work changed anything, so nothing claims it did.
	setStatus(reopened.ID, FindingDone, nil)
	publish()
	resolved := find(blockedKey)
	if resolved.Status != FindingDone {
		t.Errorf("status after a run that did not reproduce it = %s, want DONE", resolved.Status)
	}

	// A dismissal survives the finding recurring, which is the whole suppression
	// rule — no dismissed work is ever handed back.
	publish(blocked, sitemap)
	reason := "NOT_RELEVANT"
	setStatus(find(sitemapKey).ID, FindingDismissed, &reason)
	publish(blocked, sitemap)
	dismissed := find(sitemapKey)
	if dismissed.Status != FindingDismissed {
		t.Errorf("status after recurrence of a dismissed finding = %s, want DISMISSED", dismissed.Status)
	}
	if dismissed.DismissalReason == nil || *dismissed.DismissalReason != reason {
		t.Errorf("dismissal reason = %v, want %s", dismissed.DismissalReason, reason)
	}

	// Re-publishing the same monitoring run must not double-apply anything.
	if rows, err = s.ListFindings(ctx, accountID, businessID); err != nil {
		t.Fatal(err)
	}
	before := len(rows)
	lastRun := mustNewID(t)
	mustExec(t, db, ctx, "INSERT INTO monitoring_runs (id,business_id,platform,trigger,scheduled_for,status,workflow_id,completed_at,analysis_completed_at) VALUES ($1,$2,'chatgpt','scheduled','2026-06-01','completed',$3,now(),now())", lastRun, businessID, "improve-"+lastRun.String())
	for range 2 {
		if err := s.PublishImproveRun(ctx, accountID, businessID, ImproveRun{RunID: lastRun, PagesRead: 3, Findings: []visibility.Finding{blocked}}); err != nil {
			t.Fatal(err)
		}
	}
	if rows, err = s.ListFindings(ctx, accountID, businessID); err != nil || len(rows) != before {
		t.Errorf("re-publishing the same run changed the queue: %d -> %d (%v)", before, len(rows), err)
	}
	audit, assessed, err := s.PublishedAudit(ctx, accountID, businessID)
	if err != nil || !assessed || audit.PagesRead != 3 {
		t.Errorf("published audit = %+v assessed=%t err=%v", audit, assessed, err)
	}
}
