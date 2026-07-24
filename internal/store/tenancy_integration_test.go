package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"opensight/internal/domain"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestRepositoriesEnforceTenantScoping is the SCH-4 acceptance test: every
// business-owned repository rejects cross-tenant access with ErrNotFound
// (direct business lookup, deeper-table writes, and deep-by-id joins), while
// the owning tenant's calls succeed and idempotency behaves as designed.
func TestRepositoriesEnforceTenantScoping(t *testing.T) {
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
	tenantA := mustNewID(t)
	tenantB := mustNewID(t)
	businessA := mustNewID(t)
	slug := "tenancy-" + planID.String()

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM prompt_results WHERE run_id IN (SELECT id FROM monitoring_runs WHERE business_id = $1)", businessA)
		_, _ = db.ExecContext(ctx, "DELETE FROM monitoring_runs WHERE business_id = $1", businessA)
		_, _ = db.ExecContext(ctx, "DELETE FROM profile_proposals WHERE business_id = $1", businessA)
		_, _ = db.ExecContext(ctx, "DELETE FROM prompts WHERE business_id = $1", businessA)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", businessA)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = ANY($1)", []domain.ID{tenantA, tenantB})
		_, _ = db.ExecContext(ctx, "DELETE FROM plans WHERE id = $1", planID)
	})

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ($1, $2, 20, 'weekly', ARRAY['chatgpt']::text[])`,
		planID, slug,
	); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Tenant A', $2), ($3, 'Tenant B', $2)`,
		tenantA, planID, tenantB,
	); err != nil {
		t.Fatalf("insert tenants: %v", err)
	}

	businesses := NewBusinessStore(db)
	prompts := NewPromptStore(db)
	runs := NewRunStore(db)
	results := NewResultStore(db)
	proposals := NewProfileProposalStore(db)

	// --- BusinessStore: create (write) + direct tenant-scoped lookups. ---
	created, err := businesses.CreateBusiness(ctx, CreateBusinessParams{
		ID:          businessA,
		TenantID:    tenantA,
		Status:      BusinessStatusActive,
		Name:        "Acme Clinic",
		Aliases:     []string{"Acme", "ACME Clinic"},
		Category:    ptr("clinic"),
		Location:    json.RawMessage(`{"country":"SG"}`),
		ActivatedAt: ptr(time.Now().UTC()),
	})
	if err != nil {
		t.Fatalf("create business: %v", err)
	}
	if len(created.Aliases) != 2 {
		t.Fatalf("created aliases = %v, want 2 elements", created.Aliases)
	}
	updatedName, updatedCategory := "Updated Clinic", "clinic"
	updatedAliases := []string{"Updated"}
	updatedServices := json.RawMessage(`["screening"]`)
	updatedLocation := json.RawMessage(`{"country":"SG"}`)
	updated, err := businesses.UpdateActiveProfile(ctx, UpdateBusinessProfileParams{
		TenantID: tenantA, BusinessID: businessA, Name: &updatedName,
		Aliases: &updatedAliases, Category: &updatedCategory,
		Services: &updatedServices,
		Location: &updatedLocation,
	})
	if err != nil {
		t.Fatalf("UpdateActiveProfile: %v", err)
	}
	if updated.Name != "Updated Clinic" || len(updated.Services) == 0 {
		t.Fatalf("updated business = %+v", updated)
	}
	secondServices := json.RawMessage(`["screening","consultation"]`)
	disjoint, err := businesses.UpdateActiveProfile(ctx, UpdateBusinessProfileParams{
		TenantID: tenantA, BusinessID: businessA, Services: &secondServices,
	})
	if err != nil {
		t.Fatalf("disjoint UpdateActiveProfile: %v", err)
	}
	if disjoint.Name != "Updated Clinic" || string(disjoint.Services) != string(secondServices) ||
		len(disjoint.Aliases) != 1 || disjoint.Aliases[0] != "Updated" {
		t.Fatalf("disjoint update restored omitted fields: %+v", disjoint)
	}
	leaked := "Leaked"
	if _, err := businesses.UpdateActiveProfile(ctx, UpdateBusinessProfileParams{
		TenantID: tenantB, BusinessID: businessA, Name: &leaked,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant UpdateActiveProfile error = %v, want ErrNotFound", err)
	}

	if _, err := businesses.GetBusiness(ctx, tenantA, businessA); err != nil {
		t.Fatalf("GetBusiness(tenantA): %v", err)
	}
	got, err := businesses.GetBusiness(ctx, tenantB, businessA)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBusiness(tenantB) err = %v (business %+v), want ErrNotFound", err, got)
	}

	resolved, err := businesses.ResolveTenantID(ctx, businessA)
	if err != nil {
		t.Fatalf("ResolveTenantID: %v", err)
	}
	if resolved != tenantA {
		t.Fatalf("ResolveTenantID = %s, want %s", resolved, tenantA)
	}

	if list, err := businesses.ListBusinesses(ctx, tenantA); err != nil || len(list) != 1 {
		t.Fatalf("ListBusinesses(tenantA) = %d,%v, want 1,nil", len(list), err)
	}
	if list, err := businesses.ListBusinesses(ctx, tenantB); err != nil || len(list) != 0 {
		t.Fatalf("ListBusinesses(tenantB) = %d,%v, want 0,nil", len(list), err)
	}

	// --- PromptStore: deeper-table write + reads. ---
	prompt, err := prompts.CreateActivePrompt(ctx, CreateActivePromptParams{
		TenantID:   tenantA,
		BusinessID: businessA,
		Text:       "best clinic near me",
	})
	if err != nil {
		t.Fatalf("CreateActivePrompt(tenantA): %v", err)
	}

	if _, err := prompts.CreateActivePrompt(ctx, CreateActivePromptParams{
		TenantID:   tenantB,
		BusinessID: businessA,
		Text:       "cross tenant prompt",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateActivePrompt(tenantB) err = %v, want ErrNotFound", err)
	}
	// The rejected cross-tenant write must not have inserted a row.
	var promptCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM prompts WHERE business_id = $1", businessA).Scan(&promptCount); err != nil {
		t.Fatalf("count prompts: %v", err)
	}
	if promptCount != 1 {
		t.Fatalf("prompt count after rejected cross-tenant write = %d, want 1", promptCount)
	}

	if list, err := prompts.ListActivePrompts(ctx, tenantA, businessA); err != nil || len(list) != 1 {
		t.Fatalf("ListActivePrompts(tenantA) = %d,%v, want 1,nil", len(list), err)
	}
	if _, err := prompts.ListActivePrompts(ctx, tenantB, businessA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListActivePrompts(tenantB) err = %v, want ErrNotFound", err)
	}
	if _, err := prompts.GetPrompt(ctx, tenantA, prompt.ID); err != nil {
		t.Fatalf("GetPrompt(tenantA): %v", err)
	}
	if _, err := prompts.GetPrompt(ctx, tenantB, prompt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPrompt(tenantB) err = %v, want ErrNotFound", err)
	}

	// --- RunStore: idempotent upsert + tenant scoping. ---
	scheduledFor := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	run, err := runs.UpsertRun(ctx, tenantA, UpsertRunParams{
		BusinessID:   businessA,
		Platform:     "chatgpt",
		Trigger:      RunTriggerScheduled,
		ScheduledFor: scheduledFor,
		WorkflowID:   "run-" + businessA.String() + "-chatgpt-2026-07-13",
	})
	if err != nil {
		t.Fatalf("UpsertRun(tenantA): %v", err)
	}
	// Idempotency: same key converges on the same run id.
	again, err := runs.UpsertRun(ctx, tenantA, UpsertRunParams{
		BusinessID:   businessA,
		Platform:     "chatgpt",
		Trigger:      RunTriggerManual,
		ScheduledFor: scheduledFor,
		WorkflowID:   "run-duplicate",
	})
	if err != nil {
		t.Fatalf("UpsertRun(tenantA) second: %v", err)
	}
	if again.ID != run.ID {
		t.Fatalf("second UpsertRun id = %s, want %s", again.ID, run.ID)
	}
	if again.Trigger != RunTriggerScheduled {
		t.Fatalf("second UpsertRun trigger = %q, want the original %q", again.Trigger, RunTriggerScheduled)
	}

	if _, err := runs.UpsertRun(ctx, tenantB, UpsertRunParams{
		BusinessID:   businessA,
		Platform:     "chatgpt",
		Trigger:      RunTriggerManual,
		ScheduledFor: scheduledFor,
		WorkflowID:   "run-cross-tenant",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpsertRun(tenantB) err = %v, want ErrNotFound", err)
	}

	if list, err := runs.ListRuns(ctx, tenantA, businessA); err != nil || len(list) != 1 {
		t.Fatalf("ListRuns(tenantA) = %d,%v, want 1,nil", len(list), err)
	}
	if _, err := runs.ListRuns(ctx, tenantB, businessA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListRuns(tenantB) err = %v, want ErrNotFound", err)
	}

	// --- ResultStore: append-only write + deep-by-id join scoping. ---
	result, err := results.CreateResult(ctx, tenantA, CreateResultParams{
		RunID:        run.ID,
		PromptID:     prompt.ID,
		Status:       ResultStatusSucceeded,
		Model:        ptr("gpt-5-mini-2026-07-01"),
		Request:      json.RawMessage(`{"model":"gpt-5-mini"}`),
		RawResponse:  json.RawMessage(`{"id":"resp_1"}`),
		ResponseText: ptr("Acme Clinic is a good option."),
	})
	if err != nil {
		t.Fatalf("CreateResult(tenantA): %v", err)
	}

	// Duplicate (run, prompt) surfaces ErrDuplicateResult, never a silent dupe.
	if _, err := results.CreateResult(ctx, tenantA, CreateResultParams{
		RunID:        run.ID,
		PromptID:     prompt.ID,
		Status:       ResultStatusSucceeded,
		Model:        ptr("gpt-5-mini-2026-07-01"),
		Request:      json.RawMessage(`{"model":"gpt-5-mini"}`),
		RawResponse:  json.RawMessage(`{"id":"resp_2"}`),
		ResponseText: ptr("Duplicate."),
	}); !errors.Is(err, ErrDuplicateResult) {
		t.Fatalf("duplicate CreateResult err = %v, want ErrDuplicateResult", err)
	}

	if _, err := results.CreateResult(ctx, tenantB, CreateResultParams{
		RunID:    run.ID,
		PromptID: prompt.ID,
		Status:   ResultStatusFailed,
		Request:  json.RawMessage(`{"model":"gpt-5-mini"}`),
		Error:    ptr("boom"),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateResult(tenantB) err = %v, want ErrNotFound", err)
	}

	businessB := mustNewID(t)
	otherBusiness, err := businesses.CreateBusiness(ctx, CreateBusinessParams{
		ID:          businessB,
		TenantID:    tenantB,
		Status:      BusinessStatusActive,
		Name:        "Other Clinic",
		Category:    ptr("clinic"),
		Location:    json.RawMessage(`{"country":"SG"}`),
		ActivatedAt: ptr(time.Now().UTC()),
	})
	if err != nil {
		t.Fatalf("create other business: %v", err)
	}
	otherPrompt, err := prompts.CreateActivePrompt(ctx, CreateActivePromptParams{
		TenantID:   tenantB,
		BusinessID: otherBusiness.ID,
		Text:       "other clinic prompt",
	})
	if err != nil {
		t.Fatalf("CreateActivePrompt(other business): %v", err)
	}
	if _, err := results.CreateResult(ctx, tenantA, CreateResultParams{
		RunID:        run.ID,
		PromptID:     otherPrompt.ID,
		Status:       ResultStatusSucceeded,
		Model:        ptr("gpt-5-mini-2026-07-01"),
		Request:      json.RawMessage(`{"model":"gpt-5-mini"}`),
		RawResponse:  json.RawMessage(`{"id":"resp_cross_prompt"}`),
		ResponseText: ptr("Cross prompt."),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateResult with cross-business prompt err = %v, want ErrNotFound", err)
	}

	if _, err := results.GetResult(ctx, tenantA, result.ID); err != nil {
		t.Fatalf("GetResult(tenantA): %v", err)
	}
	if _, err := results.GetResult(ctx, tenantB, result.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetResult(tenantB) err = %v, want ErrNotFound", err)
	}
	detail, err := results.GetResultDetail(ctx, tenantA, result.ID)
	if err != nil {
		t.Fatalf("GetResultDetail(tenantA): %v", err)
	}
	if detail.Prompt.Text != prompt.Text || detail.Run.WorkflowID != "run-"+businessA.String()+"-chatgpt-2026-07-13" {
		t.Fatalf("GetResultDetail returned prompt/run %+v/%+v", detail.Prompt, detail.Run)
	}
	if _, err := results.GetResultDetail(ctx, tenantB, result.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetResultDetail(tenantB) err = %v, want ErrNotFound", err)
	}
	if _, err := results.GetResultByRunAndPrompt(ctx, tenantA, run.ID, prompt.ID); err != nil {
		t.Fatalf("GetResultByRunAndPrompt(tenantA): %v", err)
	}
	if _, err := results.GetResultByRunAndPrompt(ctx, tenantB, run.ID, prompt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetResultByRunAndPrompt(tenantB) err = %v, want ErrNotFound", err)
	}

	if list, err := results.ListResults(ctx, tenantA, businessA, ResultFilter{}); err != nil || len(list) != 1 {
		t.Fatalf("ListResults(tenantA) = %d,%v, want 1,nil", len(list), err)
	}
	failed := ResultStatusFailed
	if list, err := results.ListResults(ctx, tenantA, businessA, ResultFilter{Status: &failed}); err != nil || len(list) != 0 {
		t.Fatalf("ListResults(tenantA, status=failed) = %d,%v, want 0,nil", len(list), err)
	}
	if list, err := results.ListResults(ctx, tenantA, businessA, ResultFilter{RunID: &run.ID, Limit: 10}); err != nil || len(list) != 1 {
		t.Fatalf("ListResults(tenantA, run filter) = %d,%v, want 1,nil", len(list), err)
	}
	if _, err := results.ListResults(ctx, tenantB, businessA, ResultFilter{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListResults(tenantB) err = %v, want ErrNotFound", err)
	}

	// --- FinalizeRun: 1 expected + 1 succeeded -> completed; tenant scoped. ---
	finalized, err := runs.FinalizeRun(ctx, tenantA, run.ID, 1)
	if err != nil {
		t.Fatalf("FinalizeRun(tenantA): %v", err)
	}
	if finalized.Status != RunStatusCompleted {
		t.Fatalf("FinalizeRun status = %q, want %q", finalized.Status, RunStatusCompleted)
	}
	if finalized.CompletedAt == nil {
		t.Fatal("FinalizeRun completed_at is nil, want set")
	}
	if _, err := runs.FinalizeRun(ctx, tenantB, run.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FinalizeRun(tenantB) err = %v, want ErrNotFound", err)
	}

	// --- ProfileProposalStore (thin): create-pending + get-pending. ---
	if _, err := proposals.CreatePending(ctx, tenantA, businessA, json.RawMessage(`{"name":"Acme Clinic"}`)); err != nil {
		t.Fatalf("CreatePending(tenantA): %v", err)
	}
	if _, err := proposals.CreatePending(ctx, tenantA, businessA, json.RawMessage(`{"name":"Acme"}`)); !errors.Is(err, ErrPendingProposalExists) {
		t.Fatalf("second CreatePending err = %v, want ErrPendingProposalExists", err)
	}
	if _, err := proposals.CreatePending(ctx, tenantB, businessA, json.RawMessage(`{"name":"Evil"}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreatePending(tenantB) err = %v, want ErrNotFound", err)
	}
	if _, err := proposals.GetPending(ctx, tenantA, businessA); err != nil {
		t.Fatalf("GetPending(tenantA): %v", err)
	}
	if _, err := proposals.GetPending(ctx, tenantB, businessA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPending(tenantB) err = %v, want ErrNotFound", err)
	}
}

func ptr[T any](v T) *T {
	return &v
}
