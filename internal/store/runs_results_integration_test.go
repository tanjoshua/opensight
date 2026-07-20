package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestRunsResultsSchemaEnforcesIdempotencyAndAppendOnlyResults(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	planID := mustNewID(t)
	tenantID := mustNewID(t)
	businessID := mustNewID(t)
	promptID := mustNewID(t)
	runID := mustNewID(t)
	resultID := mustNewID(t)
	slug := "runs-results-" + planID.String()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM prompt_results WHERE id = $1", resultID)
		_, _ = db.ExecContext(ctx, "DELETE FROM monitoring_runs WHERE id = $1", runID)
		_, _ = db.ExecContext(ctx, "DELETE FROM prompts WHERE id = $1", promptID)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM plans WHERE id = $1", planID)
	})

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ($1, $2, 5, 'test', ARRAY['chatgpt']::text[])`,
		planID,
		slug,
	); err != nil {
		t.Fatalf("insert test plan: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO tenants (id, name, plan_id)
VALUES ($1, 'Runs Results Tenant', $2)`,
		tenantID,
		planID,
	); err != nil {
		t.Fatalf("insert test tenant: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Runs Results Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`,
		businessID,
		tenantID,
	); err != nil {
		t.Fatalf("insert test business: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO prompts (id, business_id, text, status)
VALUES ($1, $2, 'best clinic near me', 'active')`,
		promptID,
		businessID,
	); err != nil {
		t.Fatalf("insert test prompt: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO monitoring_runs (
  id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at
) VALUES (
  $1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'runs-results-workflow', now()
)`,
		runID,
		businessID,
	); err != nil {
		t.Fatalf("insert test run: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO prompt_results (
  id, run_id, prompt_id, status, model, request, raw_response, response_text
) VALUES (
  $1,
  $2,
  $3,
  'succeeded',
  'gpt-5-mini-2026-07-01',
  '{"model":"gpt-5-mini","user_location":{"country":"SG"}}'::jsonb,
  '{"id":"resp_1","model":"gpt-5-mini-2026-07-01"}'::jsonb,
  'Runs Results Clinic is a good option.'
)`,
		resultID,
		runID,
		promptID,
	); err != nil {
		t.Fatalf("insert test result: %v", err)
	}

	duplicateRunID := mustNewID(t)
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO monitoring_runs (
  id, business_id, platform, trigger, scheduled_for, status, workflow_id
) VALUES (
  $1, $2, 'chatgpt', 'manual', '2026-07-13', 'running', 'runs-results-workflow-duplicate'
)`,
		duplicateRunID,
		businessID,
	); err == nil {
		t.Fatal("duplicate monitoring run insert succeeded; want unique constraint error")
	}

	duplicateResultID := mustNewID(t)
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO prompt_results (
  id, run_id, prompt_id, status, model, request, raw_response, response_text
) VALUES (
  $1,
  $2,
  $3,
  'succeeded',
  'gpt-5-mini-2026-07-01',
  '{"model":"gpt-5-mini"}'::jsonb,
  '{"id":"resp_2"}'::jsonb,
  'Duplicate response.'
)`,
		duplicateResultID,
		runID,
		promptID,
	); err == nil {
		t.Fatal("duplicate prompt result insert succeeded; want unique constraint error")
	}

	if _, err := db.ExecContext(
		ctx,
		"UPDATE prompt_results SET response_text = 'Changed response.' WHERE id = $1",
		resultID,
	); err == nil {
		t.Fatal("prompt result update succeeded; want append-only trigger error")
	}

	// Failed results have NULL model/raw_response/response_text; reading them
	// back must survive the nullable-jsonb scan (WEB-3 regression: NULL cannot
	// scan into json.RawMessage without the nullableJSON wrapper).
	failedPromptID := mustNewID(t)
	failedResultID := mustNewID(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM prompt_results WHERE id = $1", failedResultID)
		_, _ = db.ExecContext(ctx, "DELETE FROM prompts WHERE id = $1", failedPromptID)
	})
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO prompts (id, business_id, text, status)
VALUES ($1, $2, 'cheapest clinic near me', 'active')`,
		failedPromptID,
		businessID,
	); err != nil {
		t.Fatalf("insert failed-case prompt: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO prompt_results (id, run_id, prompt_id, status, request, error)
VALUES ($1, $2, $3, 'failed', '{"model":"gpt-5-mini"}'::jsonb, 'openai: timeout')`,
		failedResultID,
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
	if _, err := resultStore.GetResultDetail(ctx, tenantID, failedResultID); err != nil {
		t.Fatalf("GetResultDetail(failed result): %v", err)
	}
}
