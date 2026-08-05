package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"opensight/internal/llm"
	"opensight/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/testsuite"
)

// countingRunner wraps a PromptRunner and records how many times RunPrompt was
// called, so the idempotency test can prove a cached result skips the LLM.
type countingRunner struct {
	inner llm.PromptRunner
	calls int
}

func (r *countingRunner) RunPrompt(ctx context.Context, req llm.PromptRequest) (llm.PromptRunResult, error) {
	r.calls++
	return r.inner.RunPrompt(ctx, req)
}

// nonRetryableRunner always fails with a non-retryable error, still returning a
// request body so ExecutePrompt can persist the failed row's NOT NULL request.
type nonRetryableRunner struct{}

func (nonRetryableRunner) RunPrompt(context.Context, llm.PromptRequest) (llm.PromptRunResult, error) {
	return llm.PromptRunResult{RequestJSON: json.RawMessage(`{"model":"fake"}`)},
		fmt.Errorf("content policy refusal: %w", llm.ErrNonRetryable)
}

// TestActivitiesAgainstPostgres exercises LoadRunSpec idempotency, ExecutePrompt
// success idempotency, and terminal-failure recording against a real database.
func TestActivitiesAgainstPostgres(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run activity integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	accountID := mustID(t)
	businessID := mustID(t)
	promptID := mustID(t)

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM prompt_results WHERE prompt_id = $1", promptID)
		_, _ = db.Exec(ctx, "DELETE FROM monitoring_runs WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE id = $1", promptID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Activities Account")
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Activities Clinic', 'clinic', '{"country":"SG","city":"Singapore"}'::jsonb, now())`, businessID, accountID)
	mustExec(t, db, ctx, "INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic near me', 'active')", promptID, businessID)

	repository := store.New(db)

	loadInput := func(date time.Time) LoadRunSpecInput {
		return LoadRunSpecInput{
			BusinessID:   businessID,
			Platform:     store.PlatformChatGPT,
			ScheduledFor: date,
			Trigger:      store.RunTriggerScheduled,
			WorkflowID:   RunWorkflowID(businessID, store.PlatformChatGPT, date),
		}
	}

	t.Run("LoadRunSpec idempotent", func(t *testing.T) {
		stub, err := llm.NewStubPromptRunner()
		if err != nil {
			t.Fatalf("stub runner: %v", err)
		}
		acts := &Activities{Store: repository, Runner: stub}
		date := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)

		first, err := acts.LoadRunSpec(ctx, loadInput(date))
		if err != nil {
			t.Fatalf("first LoadRunSpec: %v", err)
		}
		second, err := acts.LoadRunSpec(ctx, loadInput(date))
		if err != nil {
			t.Fatalf("second LoadRunSpec: %v", err)
		}
		if first.RunID != second.RunID {
			t.Fatalf("duplicate trigger produced different runs: %s vs %s", first.RunID, second.RunID)
		}
		if len(first.Prompts) != 1 || first.Prompts[0].ID != promptID {
			t.Fatalf("snapshot = %+v, want the single active prompt", first.Prompts)
		}
		if first.Location.Country != "SG" {
			t.Fatalf("location country = %q, want SG", first.Location.Country)
		}

		var count int
		if err := db.QueryRow(ctx, "SELECT count(*) FROM monitoring_runs WHERE business_id = $1 AND scheduled_for = $2", businessID, date).Scan(&count); err != nil {
			t.Fatalf("count runs: %v", err)
		}
		if count != 1 {
			t.Fatalf("monitoring_runs rows = %d, want 1", count)
		}
	})

	t.Run("ExecutePrompt success is idempotent", func(t *testing.T) {
		stub, err := llm.NewStubPromptRunner()
		if err != nil {
			t.Fatalf("stub runner: %v", err)
		}
		runner := &countingRunner{inner: stub}
		acts := &Activities{Store: repository, Runner: runner}
		date := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

		spec, err := acts.LoadRunSpec(ctx, loadInput(date))
		if err != nil {
			t.Fatalf("LoadRunSpec: %v", err)
		}
		in := ExecutePromptInput{
			AccountID: spec.AccountID,
			RunID:    spec.RunID,
			Prompt:   spec.Prompts[0],
			Location: spec.Location,
		}

		first, err := acts.ExecutePrompt(ctx, in)
		if err != nil {
			t.Fatalf("first ExecutePrompt: %v", err)
		}
		if first.Status != store.ResultStatusSucceeded {
			t.Fatalf("status = %q, want succeeded", first.Status)
		}
		second, err := acts.ExecutePrompt(ctx, in)
		if err != nil {
			t.Fatalf("second ExecutePrompt: %v", err)
		}
		if first.ResultID != second.ResultID {
			t.Fatalf("idempotent call returned a different result: %s vs %s", first.ResultID, second.ResultID)
		}
		if runner.calls != 1 {
			t.Fatalf("RunPrompt called %d times, want 1 (cached result skips the LLM)", runner.calls)
		}
	})

	t.Run("ExecutePrompt records terminal failure and returns nil", func(t *testing.T) {
		acts := &Activities{Store: repository, Runner: nonRetryableRunner{}}
		date := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)

		spec, err := acts.LoadRunSpec(ctx, loadInput(date))
		if err != nil {
			t.Fatalf("LoadRunSpec: %v", err)
		}
		in := ExecutePromptInput{
			AccountID: spec.AccountID,
			RunID:    spec.RunID,
			Prompt:   spec.Prompts[0],
			Location: spec.Location,
		}

		// Run through a real activity context so activity.GetInfo(ctx).Attempt
		// resolves (attempt 1; non-retryable makes it terminal regardless).
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()
		env.RegisterActivity(acts.ExecutePrompt)

		val, err := env.ExecuteActivity(acts.ExecutePrompt, in)
		if err != nil {
			t.Fatalf("ExecutePrompt returned error, want nil for a recorded terminal failure: %v", err)
		}
		var out ExecutePromptOutput
		if err := val.Get(&out); err != nil {
			t.Fatalf("decode output: %v", err)
		}
		if out.Status != store.ResultStatusFailed {
			t.Fatalf("status = %q, want failed", out.Status)
		}

		result, err := repository.GetResultByRunAndPrompt(ctx, spec.AccountID, spec.RunID, spec.Prompts[0].ID)
		if err != nil {
			t.Fatalf("get recorded result: %v", err)
		}
		if result.Status != store.ResultStatusFailed || result.Error == nil {
			t.Fatalf("recorded result = %+v, want a failed row with an error", result)
		}
	})

	t.Run("FinalizeRun with zero expected results is failed", func(t *testing.T) {
		stub, err := llm.NewStubPromptRunner()
		if err != nil {
			t.Fatalf("stub runner: %v", err)
		}
		acts := &Activities{Store: repository, Runner: stub}
		date := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)

		spec, err := acts.LoadRunSpec(ctx, loadInput(date))
		if err != nil {
			t.Fatalf("LoadRunSpec: %v", err)
		}

		// No results written and expected_results == 0 (LoadRunSpec's empty
		// prompt snapshot): a zero-prompt run must classify as failed (RUN-3
		// "none -> failed"), not completed.
		run, err := acts.FinalizeRun(ctx, FinalizeRunInput{
			AccountID: spec.AccountID,
			RunID:    spec.RunID,
		})
		if err != nil {
			t.Fatalf("FinalizeRun: %v", err)
		}
		if run.Status != store.RunStatusFailed {
			t.Fatalf("zero-prompt run status = %q, want failed", run.Status)
		}
	})
}

func mustExec(t *testing.T, db *pgxpool.Pool, ctx context.Context, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(ctx, query, args...); err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}
