package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
)

func TestRunsResultsSchemaEnforcesIdempotencyAndAppendOnlyResults(t *testing.T) {
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

	tenantID := mustNewID(t)
	businessID := mustNewID(t)
	promptID := mustNewID(t)
	runID := mustNewID(t)
	resultID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query171, resultID)
		_, _ = testdb.Exec(ctx, db, testdb.Query172, runID)
		_, _ = testdb.Exec(ctx, db, testdb.Query173, promptID)
		_, _ = testdb.Exec(ctx, db, testdb.Query174, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query175, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query176, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Runs Results Tenant")
	if _, err := testdb.Exec(ctx, db, testdb.Query177, businessID,
		tenantID,
	); err != nil {
		t.Fatalf("insert test business: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query178, promptID,
		businessID,
	); err != nil {
		t.Fatalf("insert test prompt: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query179, runID,
		businessID,
	); err != nil {
		t.Fatalf("insert test run: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query180, resultID,
		runID,
		promptID,
	); err != nil {
		t.Fatalf("insert test result: %v", err)
	}

	duplicateRunID := mustNewID(t)
	if _, err := testdb.Exec(ctx, db, testdb.Query181, duplicateRunID,
		businessID,
	); err == nil {
		t.Fatal("duplicate monitoring run insert succeeded; want unique constraint error")
	}

	duplicateResultID := mustNewID(t)
	if _, err := testdb.Exec(ctx, db, testdb.Query182, duplicateResultID,
		runID,
		promptID,
	); err == nil {
		t.Fatal("duplicate prompt result insert succeeded; want unique constraint error")
	}

	if _, err := testdb.Exec(ctx, db, testdb.Query183, resultID); err == nil {
		t.Fatal("prompt result update succeeded; want append-only trigger error")
	}

	// Failed results have NULL model/raw_response/response_text; reading them
	// back must survive the nullable-jsonb scan (WEB-3 regression: NULL cannot
	// scan into json.RawMessage without the nullableJSON wrapper).
	failedPromptID := mustNewID(t)
	failedResultID := mustNewID(t)
	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query184, failedResultID)
		_, _ = testdb.Exec(ctx, db, testdb.Query185, failedPromptID)
	})
	if _, err := testdb.Exec(ctx, db, testdb.Query186, failedPromptID,
		businessID,
	); err != nil {
		t.Fatalf("insert failed-case prompt: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query187, failedResultID,
		runID,
		failedPromptID,
	); err != nil {
		t.Fatalf("insert failed result: %v", err)
	}

	resultStore := NewResultStore(db)
	failedStatus := ResultStatusFailed
	list, err := resultStore.ListResults(ctx, tenantID, businessID, ResultFilter{Status: &failedStatus})
	if err != nil {
		t.Fatalf("ListResults(status=failed): %v", err)
	}
	if len(list) != 1 || list[0].RawResponse != nil || list[0].Error == nil || *list[0].Error != "openai: timeout" {
		t.Fatalf("ListResults(status=failed) = %+v, want one failed row with nil raw_response", list)
	}
	if list[0].PromptText != "cheapest clinic near me" {
		t.Fatalf("ListResults prompt text = %q, want the joined prompt text", list[0].PromptText)
	}
	// Evidence drill-downs pass aggregate result IDs in their display order.
	// Filtering must retain that order, then apply pagination to the ordered
	// set rather than falling back to response timestamps.
	ordered, err := resultStore.ListResults(ctx, tenantID, businessID, ResultFilter{
		ResultIDs: []domain.ID{failedResultID, resultID},
		Limit:     1,
		Offset:    1,
	})
	if err != nil {
		t.Fatalf("ListResults(result IDs, paged): %v", err)
	}
	if len(ordered) != 1 || ordered[0].ID != resultID {
		t.Fatalf("ListResults(result IDs, paged) = %+v, want second requested result %s", ordered, resultID)
	}
	if _, err := resultStore.GetResultDetail(ctx, tenantID, failedResultID); err != nil {
		t.Fatalf("GetResultDetail(failed result): %v", err)
	}
}

// TestFinalizeRunPartialWhenBelowExpected is RUNS-1's reason for existing: a
// run whose expected_results (the prompt-snapshot size persisted at run
// start) exceeds its succeeded count must land on partial, not completed —
// something only the DB round-trip through the stored column can prove now
// that FinalizeRun no longer takes the count as an argument.
func TestFinalizeRunPartialWhenBelowExpected(t *testing.T) {
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

	tenantID := mustNewID(t)
	businessID := mustNewID(t)
	promptID := mustNewID(t)
	runID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query188, runID)
		_, _ = testdb.Exec(ctx, db, testdb.Query189, runID)
		_, _ = testdb.Exec(ctx, db, testdb.Query190, promptID)
		_, _ = testdb.Exec(ctx, db, testdb.Query191, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query192, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query193, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Finalize Partial Tenant")
	if _, err := testdb.Exec(ctx, db, testdb.Query194, businessID, tenantID); err != nil {
		t.Fatalf("insert test business: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query195, promptID, businessID); err != nil {
		t.Fatalf("insert test prompt: %v", err)
	}

	pool := db
	runs := NewRunStore(pool)
	results := NewResultStore(pool)

	run, err := runs.UpsertRun(ctx, tenantID, UpsertRunParams{
		ID:              runID,
		BusinessID:      businessID,
		Platform:        PlatformChatGPT,
		Trigger:         RunTriggerScheduled,
		ScheduledFor:    time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		WorkflowID:      "run-finalize-partial",
		ExpectedResults: 3,
	})
	if err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}

	// Only 2 of the 3 expected results were written (the third prompt's
	// activity never completed) — the run must finalize as partial, not
	// completed. The unique (run_id, prompt_id) constraint means the two
	// succeeded results need two distinct prompts.
	if _, err := results.CreateResult(ctx, tenantID, CreateResultParams{
		ID:           mustNewID(t),
		RunID:        run.ID,
		PromptID:     promptID,
		Status:       ResultStatusSucceeded,
		Model:        ptr("gpt-5-mini-2026-07-01"),
		Request:      json.RawMessage(`{"model":"gpt-5-mini"}`),
		RawResponse:  json.RawMessage(`{"id":"resp_1"}`),
		ResponseText: ptr("A good clinic."),
		RequestedAt:  time.Now().UTC(),
		CompletedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create first result: %v", err)
	}

	secondPromptID := mustNewID(t)
	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query196, secondPromptID)
	})
	if _, err := testdb.Exec(ctx, db, testdb.Query197, secondPromptID, businessID); err != nil {
		t.Fatalf("insert second prompt: %v", err)
	}
	if _, err := results.CreateResult(ctx, tenantID, CreateResultParams{
		ID:           mustNewID(t),
		RunID:        run.ID,
		PromptID:     secondPromptID,
		Status:       ResultStatusSucceeded,
		Model:        ptr("gpt-5-mini-2026-07-01"),
		Request:      json.RawMessage(`{"model":"gpt-5-mini"}`),
		RawResponse:  json.RawMessage(`{"id":"resp_2"}`),
		ResponseText: ptr("Another good clinic."),
		RequestedAt:  time.Now().UTC(),
		CompletedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create second result: %v", err)
	}

	finalized, err := runs.FinalizeRun(ctx, tenantID, run.ID)
	if err != nil {
		t.Fatalf("FinalizeRun: %v", err)
	}
	if finalized.Status != RunStatusPartial {
		t.Fatalf("status = %q, want %q (2 succeeded of 3 expected)", finalized.Status, RunStatusPartial)
	}
}

// TestListRunsAggregatesResultCounts is RUNS-2's reason for existing: the
// LEFT JOIN LATERAL in listRunsSQL must count succeeded/failed/analyzed
// per run, scoped to that run (not the whole business) — SQL no unit test
// reaches.
func TestListRunsAggregatesResultCounts(t *testing.T) {
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

	tenantID := mustNewID(t)
	businessID := mustNewID(t)
	promptA := mustNewID(t)
	promptB := mustNewID(t)
	promptC := mustNewID(t)
	runWithResultsID := mustNewID(t)
	runWithNoResultsID := mustNewID(t)
	succeededAnalyzedID := mustNewID(t)
	succeededUnanalyzedID := mustNewID(t)
	failedID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query198, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query199, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query200, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query201, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query202, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query203, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "List Runs Counts Tenant")
	if _, err := testdb.Exec(ctx, db, testdb.Query204, businessID, tenantID); err != nil {
		t.Fatalf("insert test business: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query205, promptA, promptB, promptC, businessID); err != nil {
		t.Fatalf("insert test prompts: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query206, runWithResultsID, businessID, runWithNoResultsID); err != nil {
		t.Fatalf("insert test runs: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query207, succeededAnalyzedID, succeededUnanalyzedID, runWithResultsID, promptA, promptB); err != nil {
		t.Fatalf("insert succeeded results: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query208, failedID, runWithResultsID, promptC); err != nil {
		t.Fatalf("insert failed result: %v", err)
	}
	if _, err := testdb.Exec(ctx, db, testdb.Query209, succeededAnalyzedID); err != nil {
		t.Fatalf("insert result analysis: %v", err)
	}

	runs := NewRunStore(db)
	list, err := runs.ListRuns(ctx, tenantID, businessID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListRuns = %d runs, want 2", len(list))
	}

	// Newest scheduled_for first: runWithResultsID (07-20) then runWithNoResultsID (07-13).
	withResults, empty := list[0], list[1]
	if withResults.ID != runWithResultsID {
		t.Fatalf("list[0].ID = %s, want the run with results", withResults.ID)
	}
	if withResults.SucceededResults != 2 || withResults.FailedResults != 1 || withResults.AnalyzedResults != 1 {
		t.Fatalf("counts = %+v, want succeeded=2 failed=1 analyzed=1", withResults)
	}

	if empty.ID != runWithNoResultsID {
		t.Fatalf("list[1].ID = %s, want the run with no results", empty.ID)
	}
	if empty.SucceededResults != 0 || empty.FailedResults != 0 || empty.AnalyzedResults != 0 {
		t.Fatalf("counts = %+v, want all zero for a run with no results", empty)
	}
}
