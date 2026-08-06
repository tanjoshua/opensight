package workflows

import (
	"context"
	"os"
	"testing"
	"time"

	"opensight/internal/llm"
	"opensight/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

// TestRunWorkflowGateAgainstPostgres is BILL-7's proof (design 08 gate 3):
// a account without full access must produce zero monitoring_runs rows and
// zero LLM calls. It runs RunWorkflow against real activities (not mocked
// ones) so that a regression in the gate itself — CheckRunAccess silently
// passing, or a stage running before it — would be caught here, unlike the
// mock-only tests in run_workflow_test.go which merely assert the mocks
// weren't called.
func TestRunWorkflowGateAgainstPostgres(t *testing.T) {
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
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE id = $1", promptID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Gate Account")
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Activities Clinic', 'clinic', '{"country":"SG","city":"Singapore"}'::jsonb, now())`, businessID, accountID)
	mustExec(t, db, ctx, "INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic near me', 'active')", promptID, businessID)

	// insertAccount leaves the subscription comped (AccessFull). Flip it to a
	// never-paid state: comped=false and no Stripe subscription id at all,
	// which billing.DeriveAccess maps to AccessNever.
	subs := store.New(db)
	if err := subs.Upsert(ctx, store.UpsertSubscriptionParams{
		AccountID: accountID,
		PlanCode: "starter",
		Comped:   false,
	}); err != nil {
		t.Fatalf("downgrade subscription: %v", err)
	}

	stub, err := llm.NewStubPromptRunner()
	if err != nil {
		t.Fatalf("stub runner: %v", err)
	}
	runner := &countingRunner{inner: stub}

	acts := &Activities{Store: subs, Runner: runner}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterActivity(acts.CheckRunAccess)
	env.RegisterActivity(acts.LoadRunSpec)
	env.RegisterActivity(acts.ExecutePrompt)
	env.RegisterActivity(acts.FinalizeRun)
	// AnalyzeRun is a separate pipeline (analysis) with its own DB fixtures;
	// stubbing it here only proves it is never invoked for a skipped run.
	env.OnWorkflow(AnalyzeRun, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnWorkflow(AssessmentWorkflow, mock.Anything, mock.Anything).Return(nil).Maybe()

	scheduledFor := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	env.ExecuteWorkflow(RunWorkflow, RunWorkflowInput{
		BusinessID:   businessID,
		Platform:     store.PlatformChatGPT,
		ScheduledFor: scheduledFor,
		Trigger:      store.RunTriggerScheduled,
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error = %v, want nil (a skip is not a failure)", err)
	}
	var result RunResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("decode workflow result: %v", err)
	}
	if !result.Skipped {
		t.Fatalf("result = %+v, want Skipped = true", result)
	}

	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM monitoring_runs WHERE business_id = $1 AND scheduled_for = $2", businessID, scheduledFor).Scan(&count); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 0 {
		t.Fatalf("monitoring_runs rows = %d, want 0 (gate 3 must write nothing)", count)
	}
	if runner.calls != 0 {
		t.Fatalf("RunPrompt called %d times, want 0 (gate 3 must spend nothing)", runner.calls)
	}
}
