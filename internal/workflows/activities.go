package workflows

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"

	"go.temporal.io/sdk/temporal"
)

// Activities holds the dependencies for RunWorkflow's activities. Its methods
// are registered on the worker off a real instance; the workflow refers to them
// by function value for name resolution (see run_workflow.go).
type Activities struct {
	Businesses *store.BusinessStore
	Prompts    *store.PromptStore
	Runs       *store.RunStore
	Results    *store.ResultStore
	Runner     llm.PromptRunner
	Analysis   *store.AnalysisStore
	Extractor  llm.ExtractionRunner
}

// NewActivities returns an Activities with all dependencies wired.
func NewActivities(
	businesses *store.BusinessStore,
	prompts *store.PromptStore,
	runs *store.RunStore,
	results *store.ResultStore,
	runner llm.PromptRunner,
	analysis *store.AnalysisStore,
	extractor llm.ExtractionRunner,
) *Activities {
	return &Activities{
		Businesses: businesses,
		Prompts:    prompts,
		Runs:       runs,
		Results:    results,
		Runner:     runner,
		Analysis:   analysis,
		Extractor:  extractor,
	}
}

// PromptSnapshot is one active prompt captured at run start. The workflow
// snapshots these so a mid-run prompt replacement can't produce a half-and-half
// run (design 04).
type PromptSnapshot struct {
	ID   domain.ID
	Text string
}

// RunSpec is LoadRunSpec's output: the resolved tenant, the upserted run, the
// business location for web search, and the prompt snapshot to fan out over.
type RunSpec struct {
	TenantID domain.ID
	RunID    domain.ID
	Location llm.Location
	Prompts  []PromptSnapshot
}

// LoadRunSpecInput identifies the run to load or create.
type LoadRunSpecInput struct {
	BusinessID   domain.ID
	Platform     string
	ScheduledFor time.Time
	Trigger      store.RunTrigger
	WorkflowID   string
}

// LoadRunSpec resolves the tenant, validates business data, snapshots active
// prompts, and idempotently upserts the run row (design 04). Duplicate triggers
// for the same (business, platform, scheduled_for) converge on the existing run.
//
// Bad business data (missing/cross-tenant business, invalid location) is
// non-retryable: it won't fix itself on retry, and failing before UpsertRun
// avoids leaving a stuck running row for a data problem. Transient DB errors
// return the plain error so Temporal retries.
func (a *Activities) LoadRunSpec(ctx context.Context, in LoadRunSpecInput) (RunSpec, error) {
	tenantID, err := a.Businesses.ResolveTenantID(ctx, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return RunSpec{}, temporal.NewNonRetryableApplicationError(
				"resolve tenant for business", "BadBusinessData", err)
		}
		return RunSpec{}, err
	}

	business, err := a.Businesses.GetBusiness(ctx, tenantID, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return RunSpec{}, temporal.NewNonRetryableApplicationError(
				"load business", "BadBusinessData", err)
		}
		return RunSpec{}, err
	}

	location, err := llm.LocationFromBusinessJSON(business.Location)
	if err != nil {
		// An invalid or missing location is a profile error, not a transient
		// failure — fail before creating the run row.
		return RunSpec{}, temporal.NewNonRetryableApplicationError(
			"invalid business location", "BadBusinessData", err)
	}

	prompts, err := a.Prompts.ListActivePrompts(ctx, tenantID, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return RunSpec{}, temporal.NewNonRetryableApplicationError(
				"list active prompts", "BadBusinessData", err)
		}
		return RunSpec{}, err
	}

	run, err := a.Runs.UpsertRun(ctx, tenantID, store.UpsertRunParams{
		BusinessID:   in.BusinessID,
		Platform:     in.Platform,
		Trigger:      in.Trigger,
		ScheduledFor: in.ScheduledFor,
		WorkflowID:   in.WorkflowID,
	})
	if err != nil {
		return RunSpec{}, fmt.Errorf("upsert run: %w", err)
	}

	snapshots := make([]PromptSnapshot, 0, len(prompts))
	for _, p := range prompts {
		snapshots = append(snapshots, PromptSnapshot{ID: p.ID, Text: p.Text})
	}

	return RunSpec{
		TenantID: tenantID,
		RunID:    run.ID,
		Location: location,
		Prompts:  snapshots,
	}, nil
}

// FinalizeRunInput identifies the run to finalize and how many results were
// expected (the prompt-snapshot size).
type FinalizeRunInput struct {
	TenantID        domain.ID
	RunID           domain.ID
	ExpectedResults int
}

// FinalizeRun sets the run's terminal status from its succeeded result count
// against ExpectedResults. It is a pure recomputation, safe to retry.
func (a *Activities) FinalizeRun(ctx context.Context, in FinalizeRunInput) (store.Run, error) {
	return a.Runs.FinalizeRun(ctx, in.TenantID, in.RunID, in.ExpectedResults)
}
