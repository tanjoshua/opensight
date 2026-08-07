package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/visibility"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRebuildOpportunitiesReplacesDerivedStateAndPreservesUserState pins the
// OPP-4 projection contract across two generations: the compiler owns rank,
// presentation and currency, the user owns status/dismissal/baseline/first-seen,
// and neither half can overwrite the other. It also covers the three failures
// the incremental upsert had — nothing pruned, a met practice rewriting an open
// card, and rows outside the focus three being unreachable.
func TestRebuildOpportunitiesReplacesDerivedStateAndPreservesUserState(t *testing.T) {
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
	s := New(db)

	accountID, businessID := mustNewID(t), mustNewID(t)
	firstRunID, secondRunID := mustNewID(t), mustNewID(t)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM opportunities WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM assessment_generations WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM monitoring_runs WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})
	insertAccount(t, db, ctx, accountID, "Opportunity Rebuild Account")
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Rebuild Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`, businessID, accountID)
	for i, runID := range []domain.ID{firstRunID, secondRunID} {
		mustExec(t, db, ctx, `
			INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
			VALUES ($1, $2, 'chatgpt', 'scheduled', $3, 'completed', $4, now(), now())`,
			runID, businessID, []string{"2026-07-13", "2026-07-20"}[i], "rebuild-workflow-"+runID.String())
	}

	// Generation 1 finds three unmet practices; the user completes the first,
	// dismisses the second and leaves the third alone.
	first := mustCompile(ctx, t, s, accountID, businessID, firstRunID, "g1", "alpha", "beta", "gamma")
	before := listByPractice(ctx, t, s, accountID, businessID)
	if len(before) != 3 {
		t.Fatalf("generation 1 stored %d opportunities, want 3", len(before))
	}
	if _, err := s.SetOpportunityStatus(ctx, accountID, before["alpha"].ID, StatusCompleted, nil); err != nil {
		t.Fatalf("complete alpha: %v", err)
	}
	reason := "ALREADY_DONE"
	if _, err := s.SetOpportunityStatus(ctx, accountID, before["beta"].ID, StatusDismissed, &reason); err != nil {
		t.Fatalf("dismiss beta: %v", err)
	}

	// Generation 2 no longer detects alpha or gamma, re-ranks beta from 2 to 1
	// and finds delta.
	second := mustCompile(ctx, t, s, accountID, businessID, secondRunID, "g2", "beta", "delta")
	if second == first {
		t.Fatal("second generation reused the first generation id")
	}
	after := listByPractice(ctx, t, s, accountID, businessID)

	// gamma was never acted on and is no longer detected: it disappears rather
	// than lingering as a stale OPEN row.
	if _, ok := after["gamma"]; ok {
		t.Fatal("untouched stale opportunity gamma is still listed")
	}
	if len(after) != 3 {
		t.Fatalf("listed %d opportunities, want alpha, beta and delta", len(after))
	}

	// alpha: user state intact, still carrying its last-known presentation, no
	// longer current, and no longer holding a focus slot.
	alpha := after["alpha"]
	if alpha.Current || alpha.Focus {
		t.Fatalf("alpha current=%v focus=%v, want both false", alpha.Current, alpha.Focus)
	}
	if alpha.UserStatus != StatusCompleted {
		t.Fatalf("alpha user status = %q, want COMPLETED", alpha.UserStatus)
	}
	if len(alpha.CompletionBaseline) == 0 {
		t.Fatal("alpha lost its frozen completion baseline")
	}
	if alpha.Presentation.Title != "alpha g1" {
		t.Fatalf("alpha presentation = %q, want the last-known %q", alpha.Presentation.Title, "alpha g1")
	}
	if !alpha.FirstSeenAt.Equal(before["alpha"].FirstSeenAt) {
		t.Fatalf("alpha first_seen_at moved from %s to %s", before["alpha"].FirstSeenAt, alpha.FirstSeenAt)
	}

	// beta: derived half rebuilt (rank 2 -> 1, new presentation), user half
	// untouched, and a dismissed row never takes a focus slot.
	beta := after["beta"]
	if !beta.Current || beta.Focus {
		t.Fatalf("beta current=%v focus=%v, want current and not focus", beta.Current, beta.Focus)
	}
	if beta.Rank != 1 || beta.Presentation.Title != "beta g2" {
		t.Fatalf("beta rank=%d title=%q, want rank 1 and %q", beta.Rank, beta.Presentation.Title, "beta g2")
	}
	if beta.UserStatus != StatusDismissed || beta.DismissalReason == nil || *beta.DismissalReason != reason {
		t.Fatalf("beta user status=%q reason=%v, want DISMISSED/%s", beta.UserStatus, beta.DismissalReason, reason)
	}
	if !beta.FirstSeenAt.Equal(before["beta"].FirstSeenAt) {
		t.Fatalf("beta first_seen_at moved from %s to %s", before["beta"].FirstSeenAt, beta.FirstSeenAt)
	}

	// delta: newly compiled and actionable, so it takes a focus slot even though
	// the higher-ranked beta sits above it — focus counts actionable rows, it is
	// not a raw rank cutoff.
	delta := after["delta"]
	if !delta.Current || !delta.Focus || delta.Rank != 2 || delta.UserStatus != StatusOpen {
		t.Fatalf("delta current=%v focus=%v rank=%d status=%q, want a current rank-2 OPEN focus item", delta.Current, delta.Focus, delta.Rank, delta.UserStatus)
	}

	// The single-row read paths report the same focus flag as the list.
	detail, err := s.GetOpportunity(ctx, accountID, beta.ID)
	if err != nil {
		t.Fatalf("get beta: %v", err)
	}
	if detail.Focus != beta.Focus || detail.Current != beta.Current {
		t.Fatalf("detail focus=%v current=%v, want %v/%v", detail.Focus, detail.Current, beta.Focus, beta.Current)
	}
}

// TestSetOpportunityStatusRecordsOneEventPerTransition pins two things: one
// event per genuine transition and none for a no-op repeat, and an event key
// derived from the row's updated_at rather than from a clock read at insert
// time. The key derivation is what makes the (opportunity_id, event_key)
// constraint reachable at all; a key built from time.Now() can never collide,
// so the ON CONFLICT clause guarding it would be dead code.
func TestSetOpportunityStatusRecordsOneEventPerTransition(t *testing.T) {
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
	s := New(db)

	accountID, businessID, runID := mustNewID(t), mustNewID(t), mustNewID(t)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM opportunities WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM assessment_generations WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM monitoring_runs WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})
	insertAccount(t, db, ctx, accountID, "Opportunity Event Account")
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Event Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`, businessID, accountID)
	mustExec(t, db, ctx, `
		INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
		VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', $3, now(), now())`,
		runID, businessID, "event-workflow-"+runID.String())

	mustCompile(ctx, t, s, accountID, businessID, runID, "e1", "alpha")
	id := listByPractice(ctx, t, s, accountID, businessID)["alpha"].ID

	for _, status := range []OpportunityStatus{StatusInProgress, StatusCompleted, StatusOpen} {
		if _, err := s.SetOpportunityStatus(ctx, accountID, id, status, nil); err != nil {
			t.Fatalf("set %s: %v", status, err)
		}
		// The same transition again is a no-op and must not add a second row.
		if _, err := s.SetOpportunityStatus(ctx, accountID, id, status, nil); err != nil {
			t.Fatalf("repeat %s: %v", status, err)
		}
	}

	var events int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM opportunity_events WHERE opportunity_id = $1", id).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 3 {
		t.Fatalf("recorded %d events, want one per genuine transition (3)", events)
	}

	// The final RESTORED event's key must be reconstructible from the row it
	// describes, which a clock-derived key never is.
	final := listByPractice(ctx, t, s, accountID, businessID)["alpha"]
	wantKey := "status:OPEN:" + final.UpdatedAt.UTC().Format(time.RFC3339Nano)
	var exists bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM opportunity_events WHERE opportunity_id = $1 AND event_key = $2)", id, wantKey).Scan(&exists); err != nil {
		t.Fatalf("look up event key: %v", err)
	}
	if !exists {
		t.Fatalf("no event keyed %q; the key is not derived from the row's updated_at", wantKey)
	}
}

// mustCompile runs one generation that assesses practices as NOT_MET in the
// given order and rebuilds the projection from them, returning the generation id.
func mustCompile(ctx context.Context, t *testing.T, s *Store, accountID, businessID, runID domain.ID, label string, practices ...string) domain.ID {
	t.Helper()
	generation, err := s.StartAssessmentGeneration(ctx, accountID, businessID, runID, nil)
	if err != nil {
		t.Fatalf("start generation: %v", err)
	}
	items := make([]CompiledOpportunity, 0, len(practices))
	for _, practice := range practices {
		draft := visibility.AssessmentDraft{PracticeKey: practice, CriteriaVersion: 1, AssessorKey: "test", AssessorVersion: 1, SubjectKey: "subject", Status: visibility.StatusNotMet, CheckedSources: []string{}, Explanation: practice + " is unmet", PayloadVersion: 1, Payload: json.RawMessage(`{}`)}
		assessmentID, err := s.SaveAssessment(ctx, generation.ID, accountID, businessID, draft)
		if err != nil {
			t.Fatalf("save %s assessment: %v", practice, err)
		}
		title := practice + " " + label
		items = append(items, CompiledOpportunity{AssessmentID: assessmentID, CompiledAssessment: visibility.CompiledAssessment{Draft: draft, Presentation: visibility.Presentation{Title: title, Summary: title, Effort: "Small"}}})
	}
	if err := s.RebuildOpportunities(ctx, accountID, businessID, generation.ID, items); err != nil {
		t.Fatalf("rebuild opportunities: %v", err)
	}
	return generation.ID
}

func listByPractice(ctx context.Context, t *testing.T, s *Store, accountID, businessID domain.ID) map[string]OpportunityRecord {
	t.Helper()
	rows, err := s.ListOpportunities(ctx, accountID, businessID)
	if err != nil {
		t.Fatalf("list opportunities: %v", err)
	}
	out := make(map[string]OpportunityRecord, len(rows))
	for _, row := range rows {
		out[row.PracticeKey] = row
	}
	return out
}
