package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRepositoriesEnforceAccountScoping is the SCH-4 acceptance test: every
// business-owned repository rejects cross-account access with ErrNotFound
// (direct business lookup, deeper-table writes, and deep-by-id joins), while
// the owning account's calls succeed and idempotency behaves as designed.
func TestRepositoriesEnforceAccountScoping(t *testing.T) {
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

	accountA := mustNewID(t)
	accountB := mustNewID(t)
	businessA := mustNewID(t)

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM prompt_results WHERE run_id IN (SELECT id FROM monitoring_runs WHERE business_id = $1)", businessA)
		_, _ = db.Exec(ctx, "DELETE FROM monitoring_runs WHERE business_id = $1", businessA)
		_, _ = db.Exec(ctx, "DELETE FROM profile_proposals WHERE business_id = $1", businessA)
		_, _ = db.Exec(ctx, "DELETE FROM prompts WHERE business_id = $1", businessA)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessA)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = ANY($1)", []domain.ID{accountA, accountB})
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = ANY($1)", []domain.ID{accountA, accountB})
	})

	insertAccount(t, db, ctx, accountA, "Account A")
	insertAccount(t, db, ctx, accountB, "Account B")

	pool := db
	businesses := New(pool)
	prompts := New(pool)
	runs := New(pool)
	results := New(pool)
	proposals := New(pool)

	// --- BusinessStore: create (write) + direct account-scoped lookups. ---
	created, err := businesses.CreateBusiness(ctx, CreateBusinessParams{
		ID:          businessA,
		AccountID:    accountA,
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
	// CreateBusiness renames the owning account atomically with the business
	// insert (BILL-3): onboarding's first business replaces signup's
	// email-local-part placeholder name with the real business name.
	var accountAName string
	if err := db.QueryRow(ctx, "SELECT name FROM accounts WHERE id = $1", accountA).Scan(&accountAName); err != nil {
		t.Fatalf("load account name: %v", err)
	}
	if accountAName != "Acme Clinic" {
		t.Fatalf("account name after CreateBusiness = %q, want %q", accountAName, "Acme Clinic")
	}
	updatedName, updatedCategory := "Updated Clinic", "clinic"
	updatedAliases := []string{"Updated"}
	updatedServices := json.RawMessage(`["screening"]`)
	updatedLocation := json.RawMessage(`{"country":"SG"}`)
	updated, err := businesses.UpdateActiveProfile(ctx, UpdateBusinessProfileParams{
		AccountID: accountA, BusinessID: businessA, Name: &updatedName,
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
		AccountID: accountA, BusinessID: businessA, Services: &secondServices,
	})
	if err != nil {
		t.Fatalf("disjoint UpdateActiveProfile: %v", err)
	}
	var gotServices, wantServices []string
	if err := json.Unmarshal(disjoint.Services, &gotServices); err != nil {
		t.Fatalf("decode updated services: %v", err)
	}
	if err := json.Unmarshal(secondServices, &wantServices); err != nil {
		t.Fatalf("decode expected services: %v", err)
	}
	if disjoint.Name != "Updated Clinic" || !slices.Equal(gotServices, wantServices) ||
		len(disjoint.Aliases) != 1 || disjoint.Aliases[0] != "Updated" {
		t.Fatalf("disjoint update restored omitted fields: %+v", disjoint)
	}
	leaked := "Leaked"
	if _, err := businesses.UpdateActiveProfile(ctx, UpdateBusinessProfileParams{
		AccountID: accountB, BusinessID: businessA, Name: &leaked,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account UpdateActiveProfile error = %v, want ErrNotFound", err)
	}

	if _, err := businesses.GetBusiness(ctx, accountA, businessA); err != nil {
		t.Fatalf("GetBusiness(accountA): %v", err)
	}
	got, err := businesses.GetBusiness(ctx, accountB, businessA)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBusiness(accountB) err = %v (business %+v), want ErrNotFound", err, got)
	}

	resolved, err := businesses.ResolveAccountID(ctx, businessA)
	if err != nil {
		t.Fatalf("ResolveAccountID: %v", err)
	}
	if resolved != accountA {
		t.Fatalf("ResolveAccountID = %s, want %s", resolved, accountA)
	}

	if list, err := businesses.ListBusinesses(ctx, accountA); err != nil || len(list) != 1 {
		t.Fatalf("ListBusinesses(accountA) = %d,%v, want 1,nil", len(list), err)
	}
	if list, err := businesses.ListBusinesses(ctx, accountB); err != nil || len(list) != 0 {
		t.Fatalf("ListBusinesses(accountB) = %d,%v, want 0,nil", len(list), err)
	}

	// --- PromptStore: deeper-table write + reads. ---
	prompt, err := prompts.CreateActivePrompt(ctx, CreateActivePromptParams{
		AccountID:   accountA,
		BusinessID: businessA,
		Text:       "best clinic near me",
	})
	if err != nil {
		t.Fatalf("CreateActivePrompt(accountA): %v", err)
	}

	if _, err := prompts.CreateActivePrompt(ctx, CreateActivePromptParams{
		AccountID:   accountB,
		BusinessID: businessA,
		Text:       "cross account prompt",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateActivePrompt(accountB) err = %v, want ErrNotFound", err)
	}
	// The rejected cross-account write must not have inserted a row.
	var promptCount int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM prompts WHERE business_id = $1", businessA).Scan(&promptCount); err != nil {
		t.Fatalf("count prompts: %v", err)
	}
	if promptCount != 1 {
		t.Fatalf("prompt count after rejected cross-account write = %d, want 1", promptCount)
	}

	if list, err := prompts.ListActivePrompts(ctx, accountA, businessA); err != nil || len(list) != 1 {
		t.Fatalf("ListActivePrompts(accountA) = %d,%v, want 1,nil", len(list), err)
	}
	if _, err := prompts.ListActivePrompts(ctx, accountB, businessA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListActivePrompts(accountB) err = %v, want ErrNotFound", err)
	}
	if _, err := prompts.GetPrompt(ctx, accountA, prompt.ID); err != nil {
		t.Fatalf("GetPrompt(accountA): %v", err)
	}
	if _, err := prompts.GetPrompt(ctx, accountB, prompt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPrompt(accountB) err = %v, want ErrNotFound", err)
	}

	// --- RunStore: idempotent upsert + account scoping. ---
	scheduledFor := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	run, err := runs.UpsertRun(ctx, accountA, UpsertRunParams{
		BusinessID:      businessA,
		Platform:        "chatgpt",
		Trigger:         RunTriggerScheduled,
		ScheduledFor:    scheduledFor,
		WorkflowID:      "run-" + businessA.String() + "-chatgpt-2026-07-13",
		ExpectedResults: 1,
	})
	if err != nil {
		t.Fatalf("UpsertRun(accountA): %v", err)
	}
	// Idempotency: same key converges on the same run id.
	again, err := runs.UpsertRun(ctx, accountA, UpsertRunParams{
		BusinessID:   businessA,
		Platform:     "chatgpt",
		Trigger:      RunTriggerManual,
		ScheduledFor: scheduledFor,
		WorkflowID:   "run-duplicate",
	})
	if err != nil {
		t.Fatalf("UpsertRun(accountA) second: %v", err)
	}
	if again.ID != run.ID {
		t.Fatalf("second UpsertRun id = %s, want %s", again.ID, run.ID)
	}
	if again.Trigger != RunTriggerScheduled {
		t.Fatalf("second UpsertRun trigger = %q, want the original %q", again.Trigger, RunTriggerScheduled)
	}

	if _, err := runs.UpsertRun(ctx, accountB, UpsertRunParams{
		BusinessID:   businessA,
		Platform:     "chatgpt",
		Trigger:      RunTriggerManual,
		ScheduledFor: scheduledFor,
		WorkflowID:   "run-cross-account",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpsertRun(accountB) err = %v, want ErrNotFound", err)
	}

	if list, err := runs.ListRuns(ctx, accountA, businessA); err != nil || len(list) != 1 {
		t.Fatalf("ListRuns(accountA) = %d,%v, want 1,nil", len(list), err)
	}
	if _, err := runs.ListRuns(ctx, accountB, businessA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListRuns(accountB) err = %v, want ErrNotFound", err)
	}

	// --- ResultStore: append-only write + deep-by-id join scoping. ---
	result, err := results.CreateResult(ctx, accountA, CreateResultParams{
		RunID:        run.ID,
		PromptID:     prompt.ID,
		Status:       ResultStatusSucceeded,
		Model:        ptr("gpt-5-mini-2026-07-01"),
		Request:      json.RawMessage(`{"model":"gpt-5-mini"}`),
		RawResponse:  json.RawMessage(`{"id":"resp_1"}`),
		ResponseText: ptr("Acme Clinic is a good option."),
	})
	if err != nil {
		t.Fatalf("CreateResult(accountA): %v", err)
	}

	// Duplicate (run, prompt) surfaces ErrDuplicateResult, never a silent dupe.
	if _, err := results.CreateResult(ctx, accountA, CreateResultParams{
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

	if _, err := results.CreateResult(ctx, accountB, CreateResultParams{
		RunID:    run.ID,
		PromptID: prompt.ID,
		Status:   ResultStatusFailed,
		Request:  json.RawMessage(`{"model":"gpt-5-mini"}`),
		Error:    ptr("boom"),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateResult(accountB) err = %v, want ErrNotFound", err)
	}

	businessB := mustNewID(t)
	otherBusiness, err := businesses.CreateBusiness(ctx, CreateBusinessParams{
		ID:          businessB,
		AccountID:    accountB,
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
		AccountID:   accountB,
		BusinessID: otherBusiness.ID,
		Text:       "other clinic prompt",
	})
	if err != nil {
		t.Fatalf("CreateActivePrompt(other business): %v", err)
	}
	if _, err := results.CreateResult(ctx, accountA, CreateResultParams{
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

	if _, err := results.GetResult(ctx, accountA, result.ID); err != nil {
		t.Fatalf("GetResult(accountA): %v", err)
	}
	if _, err := results.GetResult(ctx, accountB, result.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetResult(accountB) err = %v, want ErrNotFound", err)
	}
	detail, err := results.GetResultDetail(ctx, accountA, result.ID)
	if err != nil {
		t.Fatalf("GetResultDetail(accountA): %v", err)
	}
	if detail.Prompt.Text != prompt.Text || detail.Run.WorkflowID != "run-"+businessA.String()+"-chatgpt-2026-07-13" {
		t.Fatalf("GetResultDetail returned prompt/run %+v/%+v", detail.Prompt, detail.Run)
	}
	if _, err := results.GetResultDetail(ctx, accountB, result.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetResultDetail(accountB) err = %v, want ErrNotFound", err)
	}
	if _, err := results.GetResultByRunAndPrompt(ctx, accountA, run.ID, prompt.ID); err != nil {
		t.Fatalf("GetResultByRunAndPrompt(accountA): %v", err)
	}
	if _, err := results.GetResultByRunAndPrompt(ctx, accountB, run.ID, prompt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetResultByRunAndPrompt(accountB) err = %v, want ErrNotFound", err)
	}

	if list, err := results.ListResults(ctx, accountA, businessA, ResultFilter{ResultIDs: []domain.ID{result.ID}}); err != nil || len(list) != 1 {
		t.Fatalf("ListResults(accountA, result IDs) = %d,%v, want 1,nil", len(list), err)
	}
	failed := ResultStatusFailed
	if list, err := results.ListResults(ctx, accountA, businessA, ResultFilter{Status: &failed}); err != nil || len(list) != 0 {
		t.Fatalf("ListResults(accountA, status=failed) = %d,%v, want 0,nil", len(list), err)
	}
	if list, err := results.ListResults(ctx, accountA, businessA, ResultFilter{RunID: &run.ID, Limit: 10}); err != nil || len(list) != 1 {
		t.Fatalf("ListResults(accountA, run filter) = %d,%v, want 1,nil", len(list), err)
	}
	if _, err := results.ListResults(ctx, accountB, businessA, ResultFilter{ResultIDs: []domain.ID{result.ID}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListResults(accountB, result IDs) err = %v, want ErrNotFound", err)
	}

	// --- FinalizeRun: 1 expected (from the row) + 1 succeeded -> completed; account scoped. ---
	finalized, err := runs.FinalizeRun(ctx, accountA, run.ID)
	if err != nil {
		t.Fatalf("FinalizeRun(accountA): %v", err)
	}
	if finalized.Status != RunStatusCompleted {
		t.Fatalf("FinalizeRun status = %q, want %q", finalized.Status, RunStatusCompleted)
	}
	if finalized.CompletedAt == nil {
		t.Fatal("FinalizeRun completed_at is nil, want set")
	}
	if _, err := runs.FinalizeRun(ctx, accountB, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FinalizeRun(accountB) err = %v, want ErrNotFound", err)
	}

	// --- ProfileProposalStore (thin): create-pending + get-pending. ---
	if _, err := proposals.CreatePending(ctx, accountA, businessA, json.RawMessage(`{"name":"Acme Clinic"}`)); err != nil {
		t.Fatalf("CreatePending(accountA): %v", err)
	}
	if _, err := proposals.CreatePending(ctx, accountA, businessA, json.RawMessage(`{"name":"Acme"}`)); !errors.Is(err, ErrPendingProposalExists) {
		t.Fatalf("second CreatePending err = %v, want ErrPendingProposalExists", err)
	}
	if _, err := proposals.CreatePending(ctx, accountB, businessA, json.RawMessage(`{"name":"Evil"}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreatePending(accountB) err = %v, want ErrNotFound", err)
	}
	if _, err := proposals.GetPending(ctx, accountA, businessA); err != nil {
		t.Fatalf("GetPending(accountA): %v", err)
	}
	if _, err := proposals.GetPending(ctx, accountB, businessA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPending(accountB) err = %v, want ErrNotFound", err)
	}
}

func ptr[T any](v T) *T {
	return &v
}
