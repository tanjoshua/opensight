package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"opensight/internal/visibility"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestImprovementActionCyclesRequireRegressionOrChangedRecommendation(t *testing.T) {
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
	insertAccount(t, db, ctx, accountID, "Improve Cycle Account")
	mustExec(t, db, ctx, "INSERT INTO businesses (id,account_id,status,name,category,location,activated_at) VALUES ($1,$2,'active','Cycle Clinic','clinic','{\"country\":\"SG\"}'::jsonb,now())", businessID, accountID)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id=$1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id=$1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id=$1", accountID)
	})

	runNumber := 0
	publish := func(status visibility.AssessmentStatus, payload string) {
		t.Helper()
		runNumber++
		runID := mustNewID(t)
		scheduled := time.Date(2026, 1, 1+runNumber, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		mustExec(t, db, ctx, "INSERT INTO monitoring_runs (id,business_id,platform,trigger,scheduled_for,status,workflow_id,completed_at,analysis_completed_at) VALUES ($1,$2,'chatgpt','scheduled',$3,'completed',$4,now(),now())", runID, businessID, scheduled, "improve-"+runID.String())
		generation, err := s.StartAssessmentGeneration(ctx, accountID, businessID, runID, nil)
		if err != nil {
			t.Fatal(err)
		}
		draft := visibility.AssessmentDraft{PracticeKey: visibility.PracticeSearchAccess, CriteriaVersion: 1, AssessorKey: "search-access", AssessorVersion: 1, SubjectKey: visibility.BusinessSubjectKey, Status: status, Explanation: "checked", Reach: 1, Persistence: 1, EvidenceQuality: 1, Actionability: 1, Effort: 1, PayloadVersion: 1, Payload: json.RawMessage(payload)}
		assessmentID, err := s.SaveAssessment(ctx, generation.ID, accountID, businessID, draft)
		if err != nil {
			t.Fatal(err)
		}
		items := []CompiledAction{}
		if visibility.Eligible(draft) {
			items = append(items, CompiledAction{AssessmentID: assessmentID, CompiledAssessment: visibility.CompiledAssessment{Draft: draft, Presentation: visibility.Presentation{Title: "Search access", Summary: "checked", Effort: "Small"}}})
		}
		if err := s.PublishAssessmentGeneration(ctx, accountID, businessID, generation.ID, []ModuleOutcome{{AssessorKey: "search-access", Status: "SUCCEEDED"}}, items); err != nil {
			t.Fatal(err)
		}
	}

	publish(visibility.StatusNotMet, `{"barrier":true}`)
	actions, err := s.ListActions(ctx, accountID, businessID)
	if err != nil || len(actions) != 1 {
		t.Fatalf("first actions=%d err=%v", len(actions), err)
	}
	first := actions[0]
	if _, err = s.SetActionStatus(ctx, accountID, first.ID, ActionInProgress, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetActionStatus(ctx, accountID, first.ID, ActionCompleted, nil); err != nil {
		t.Fatal(err)
	}
	completed, err := s.GetAction(ctx, accountID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	baseline := string(completed.CompletionBaseline)
	if baseline == "" {
		t.Fatal("completion baseline was not frozen")
	}

	// A running generation is invisible, and a failed assessor publishes no
	// replacement scope. The prior standing remains readable and becomes stale.
	runNumber++
	failedRunID := mustNewID(t)
	failedDate := time.Date(2026, 1, 1+runNumber, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	mustExec(t, db, ctx, "INSERT INTO monitoring_runs (id,business_id,platform,trigger,scheduled_for,status,workflow_id,completed_at,analysis_completed_at) VALUES ($1,$2,'chatgpt','scheduled',$3,'completed',$4,now(),now())", failedRunID, businessID, failedDate, "improve-"+failedRunID.String())
	failedGeneration, err := s.StartAssessmentGeneration(ctx, accountID, businessID, failedRunID, nil)
	if err != nil {
		t.Fatal(err)
	}
	beforePublish, _, err := s.PublishedAssessments(ctx, accountID, businessID)
	if err != nil || len(beforePublish) != 1 || beforePublish[0].GenerationID == failedGeneration.ID {
		t.Fatalf("running generation leaked into reads: rows=%v err=%v", beforePublish, err)
	}
	if err = s.PublishAssessmentGeneration(ctx, accountID, businessID, failedGeneration.ID, []ModuleOutcome{{AssessorKey: "search-access", Status: "FAILED", Error: "inspection failed"}}, nil); err != nil {
		t.Fatal(err)
	}
	preserved, freshness, err := s.PublishedAssessments(ctx, accountID, businessID)
	if err != nil || len(preserved) != 1 || !freshness.Partial || !freshness.Stale {
		t.Fatalf("failed scope was not preserved stale: rows=%v freshness=%+v err=%v", preserved, freshness, err)
	}
	publish(visibility.StatusNotMet, `{"barrier":true}`)
	actions, _ = s.ListActions(ctx, accountID, businessID)
	if len(actions) != 1 {
		t.Fatalf("unchanged actionable assessment created a cycle: %d actions", len(actions))
	}
	publish(visibility.StatusMet, `{"barrier":false}`)
	publish(visibility.StatusNotMet, `{"barrier":true}`)
	actions, _ = s.ListActions(ctx, accountID, businessID)
	if len(actions) != 2 || actions[0].Cycle != 2 {
		t.Fatalf("verified regression actions=%v", actions)
	}
	second := actions[0]
	reason := "NOT_RELEVANT"
	if _, err = s.SetActionStatus(ctx, accountID, second.ID, ActionDismissed, &reason); err != nil {
		t.Fatal(err)
	}
	publish(visibility.StatusNotMet, `{"barrier":true}`)
	actions, _ = s.ListActions(ctx, accountID, businessID)
	if len(actions) != 2 {
		t.Fatalf("dismissed identical recommendation was not suppressed: %d", len(actions))
	}
	publish(visibility.StatusNotMet, `{"barrier":true,"kind":"robots"}`)
	actions, _ = s.ListActions(ctx, accountID, businessID)
	if len(actions) != 3 || actions[0].Cycle != 3 {
		t.Fatalf("changed recommendation did not recur: %v", actions)
	}
	completed, err = s.GetAction(ctx, accountID, first.ID)
	if err != nil || string(completed.CompletionBaseline) != baseline {
		t.Fatalf("completion baseline changed after reassessment: %v", err)
	}
	page1, total, err := s.ListActivity(ctx, accountID, businessID, 2, 0)
	if err != nil || len(page1) != 2 || total < 7 {
		t.Fatalf("activity first page len=%d total=%d err=%v", len(page1), total, err)
	}
	page2, secondTotal, err := s.ListActivity(ctx, accountID, businessID, 2, 2)
	if err != nil || len(page2) != 2 || secondTotal != total || page1[1].ID == page2[0].ID {
		t.Fatalf("activity pagination invalid: page2=%v total=%d err=%v", page2, secondTotal, err)
	}
	if _, err = s.GetAction(ctx, mustNewID(t), actions[0].ID); err != ErrNotFound {
		t.Fatalf("cross-account action read error=%v, want ErrNotFound", err)
	}
}
