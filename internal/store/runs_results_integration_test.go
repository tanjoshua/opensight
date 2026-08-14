package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
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

	accountID := mustNewID(t)
	businessID := mustNewID(t)
	promptID := mustNewID(t)
	runID := mustNewID(t)
	resultID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM prompt_results WHERE id = $1", resultID)
		_, _ = db.Exec(ctx, "DELETE FROM monitoring_runs WHERE id = $1", runID)
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE id = $1", promptID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Runs Results Account")
	if _, err := db.Exec(ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Runs Results Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`, businessID, accountID); err != nil {
		t.Fatalf("insert test business: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO prompts (id, business_id, text, status)
		VALUES ($1, $2, 'best clinic near me', 'active')`, promptID, businessID); err != nil {
		t.Fatalf("insert test prompt: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, job_id, completed_at)
		VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 501, now())`, runID, businessID); err != nil {
		t.Fatalf("insert test run: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
		VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini-2026-07-01',
		        '{"model":"gpt-5-mini","user_location":{"country":"SG"}}'::jsonb,
		        '{"id":"resp_1","model":"gpt-5-mini-2026-07-01"}'::jsonb,
		        'Runs Results Clinic is a good option.')`, resultID, runID, promptID); err != nil {
		t.Fatalf("insert test result: %v", err)
	}

	duplicateRunID := mustNewID(t)
	if _, err := db.Exec(ctx, `
		INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, job_id)
		VALUES ($1, $2, 'chatgpt', 'manual', '2026-07-13', 'running', 502)`, duplicateRunID, businessID); err == nil {
		t.Fatal("duplicate monitoring run insert succeeded; want unique constraint error")
	}

	duplicateResultID := mustNewID(t)
	if _, err := db.Exec(ctx, `
		INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
		VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini-2026-07-01',
		        '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_2"}'::jsonb, 'Duplicate response.')`, duplicateResultID, runID, promptID); err == nil {
		t.Fatal("duplicate prompt result insert succeeded; want unique constraint error")
	}

	if _, err := db.Exec(ctx, "UPDATE prompt_results SET response_text = 'Changed response.' WHERE id = $1", resultID); err == nil {
		t.Fatal("prompt result update succeeded; want append-only trigger error")
	}

	// Failed results have NULL model/raw_response/response_text; reading them
	// back must survive the nullable-jsonb scan (WEB-3 regression: NULL cannot
	// scan into json.RawMessage without the nullableJSON wrapper).
	failedPromptID := mustNewID(t)
	failedResultID := mustNewID(t)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM prompt_results WHERE id = $1", failedResultID)
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE id = $1", failedPromptID)
	})
	if _, err := db.Exec(ctx, `
		INSERT INTO prompts (id, business_id, text, status)
		VALUES ($1, $2, 'cheapest clinic near me', 'active')`, failedPromptID, businessID); err != nil {
		t.Fatalf("insert failed-case prompt: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO prompt_results (id, run_id, prompt_id, status, request, error)
		VALUES ($1, $2, $3, 'failed', '{"model":"gpt-5-mini"}'::jsonb, 'openai: timeout')`, failedResultID, runID, failedPromptID); err != nil {
		t.Fatalf("insert failed result: %v", err)
	}

	resultStore := New(db)
	failedStatus := ResultStatusFailed
	list, err := resultStore.ListResults(ctx, accountID, businessID, ResultFilter{Status: &failedStatus})
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
	ordered, err := resultStore.ListResults(ctx, accountID, businessID, ResultFilter{
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
	if _, err := resultStore.GetResultDetail(ctx, accountID, failedResultID); err != nil {
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

	accountID := mustNewID(t)
	businessID := mustNewID(t)
	promptID := mustNewID(t)
	runID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM prompt_results WHERE run_id = $1", runID)
		_, _ = db.Exec(ctx, "DELETE FROM monitoring_runs WHERE id = $1", runID)
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE id = $1", promptID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Finalize Partial Account")
	if _, err := db.Exec(ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Finalize Partial Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`, businessID, accountID); err != nil {
		t.Fatalf("insert test business: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO prompts (id, business_id, text, status)
		VALUES ($1, $2, 'best clinic near me', 'active')`, promptID, businessID); err != nil {
		t.Fatalf("insert test prompt: %v", err)
	}

	pool := db
	runs := New(pool)
	results := New(pool)

	run, err := runs.UpsertRun(ctx, accountID, UpsertRunParams{
		ID:              runID,
		BusinessID:      businessID,
		Platform:        PlatformChatGPT,
		Trigger:         RunTriggerScheduled,
		ScheduledFor:    time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		JobID:           104,
		ExpectedResults: 3,
		Spec:            json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}

	// Only 2 of the 3 expected results were written (the third prompt's
	// activity never completed) — the run must finalize as partial, not
	// completed. The unique (run_id, prompt_id) constraint means the two
	// succeeded results need two distinct prompts.
	if _, err := results.CreateResult(ctx, accountID, CreateResultParams{
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
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE id = $1", secondPromptID)
	})
	if _, err := db.Exec(ctx, `
		INSERT INTO prompts (id, business_id, text, status)
		VALUES ($1, $2, 'cheapest clinic near me', 'active')`, secondPromptID, businessID); err != nil {
		t.Fatalf("insert second prompt: %v", err)
	}
	if _, err := results.CreateResult(ctx, accountID, CreateResultParams{
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

	finalized, err := runs.FinalizeRun(ctx, accountID, run.ID)
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

	accountID := mustNewID(t)
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
		_, _ = db.Exec(ctx, "DELETE FROM prompt_results WHERE run_id IN (SELECT id FROM monitoring_runs WHERE business_id = $1)", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM monitoring_runs WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "List Runs Counts Account")
	if _, err := db.Exec(ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'List Runs Counts Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`, businessID, accountID); err != nil {
		t.Fatalf("insert test business: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO prompts (id, business_id, text, status)
		VALUES ($1, $4, 'a', 'active'), ($2, $4, 'b', 'active'), ($3, $4, 'c', 'active')`, promptA, promptB, promptC, businessID); err != nil {
		t.Fatalf("insert test prompts: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, job_id, expected_results, completed_at)
		VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-20', 'partial', 503, 3, now()),
		       ($3, $2, 'chatgpt', 'scheduled', '2026-07-13', 'running', 504, 2, NULL)`, runWithResultsID, businessID, runWithNoResultsID); err != nil {
		t.Fatalf("insert test runs: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
		VALUES ($1, $3, $4, 'succeeded', 'gpt-5-mini-2026-07-01', '{}'::jsonb, '{}'::jsonb, 'ok'),
		       ($2, $3, $5, 'succeeded', 'gpt-5-mini-2026-07-01', '{}'::jsonb, '{}'::jsonb, 'ok')`, succeededAnalyzedID, succeededUnanalyzedID, runWithResultsID, promptA, promptB); err != nil {
		t.Fatalf("insert succeeded results: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO prompt_results (id, run_id, prompt_id, status, request, error)
		VALUES ($1, $2, $3, 'failed', '{}'::jsonb, 'openai: timeout')`, failedID, runWithResultsID, promptC); err != nil {
		t.Fatalf("insert failed result: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO result_analyses (prompt_result_id, analysis_model, extraction_version)
		VALUES ($1, 'gpt-5-mini-2026-07-01', 1)`, succeededAnalyzedID); err != nil {
		t.Fatalf("insert result analysis: %v", err)
	}

	runs := New(db)
	list, err := runs.ListRuns(ctx, accountID, businessID)
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
