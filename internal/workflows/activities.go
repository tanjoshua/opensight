package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"

	"go.temporal.io/sdk/temporal"
)

// Activities holds the dependencies for RunWorkflow's activities. Its methods
// are registered on the worker off a real instance; the workflow refers to them
// by function value for name resolution (see run_workflow.go). Construct it
// with a struct literal naming only the fields a given caller needs — tests
// exercising one activity leave the rest at their nil zero value instead of
// padding a long positional constructor call.
type Activities struct {
	Businesses    *store.BusinessStore
	Prompts       *store.PromptStore
	Runs          *store.RunStore
	Results       *store.ResultStore
	Runner        llm.PromptRunner
	Analysis      *store.AnalysisStore
	Extractor     llm.ExtractionRunner
	Matcher       llm.MatchRunner
	Proposer      llm.ProposeProfileRunner
	Proposals     *store.ProfileProposalStore
	Subscriptions *store.SubscriptionStore
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

// CheckRunAccessInput identifies the business whose tenant access gates the run.
type CheckRunAccessInput struct {
	BusinessID domain.ID
}

// CheckRunAccessOutput carries the resolved tenant and its access, so a caller
// that skips the run still has the tenant id for logging without a second
// lookup. Access is the string form (billing.Access.String()) rather than the
// int, so the value is legible in Temporal history and workflow results —
// AccessNever is the zero value of the int, which would otherwise read as
// "unset" rather than "never paid".
type CheckRunAccessOutput struct {
	TenantID domain.ID
	Access   string
}

// CheckRunAccess is design 08's gate 3, the authoritative spend backstop:
// RunWorkflow calls this before LoadRunSpec (the only monitoring_runs writer)
// and before any prompt executes, so a tenant without full access costs
// nothing — no run row, no prompt, no analysis. Gates 1 (RPC) and 2 (schedule
// pause) both depend on a webhook that can be delayed or dropped; this one
// recomputes access fresh against the current time on every run start, so a
// missed webhook can never turn into spend.
func (a *Activities) CheckRunAccess(ctx context.Context, in CheckRunAccessInput) (CheckRunAccessOutput, error) {
	tenantID, err := a.Businesses.ResolveTenantID(ctx, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return CheckRunAccessOutput{}, temporal.NewNonRetryableApplicationError(
				"resolve tenant for business", "BadBusinessData", err)
		}
		return CheckRunAccessOutput{}, err
	}

	sub, err := a.Subscriptions.GetByTenant(ctx, tenantID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Every tenant is supposed to have exactly one subscriptions row
			// (design 08); a miss is a bug, not a normal skip. Non-retryable so
			// it surfaces rather than retrying forever, and it still spends
			// nothing.
			return CheckRunAccessOutput{}, temporal.NewNonRetryableApplicationError(
				"load subscription for tenant", "BadBillingData", err)
		}
		return CheckRunAccessOutput{}, err
	}

	access := billing.DeriveAccess(sub.AccessState(), time.Now().UTC())
	return CheckRunAccessOutput{TenantID: tenantID, Access: access.String()}, nil
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

	snapshots := make([]PromptSnapshot, 0, len(prompts))
	for _, p := range prompts {
		snapshots = append(snapshots, PromptSnapshot{ID: p.ID, Text: p.Text})
	}

	run, err := a.Runs.UpsertRun(ctx, tenantID, store.UpsertRunParams{
		BusinessID:      in.BusinessID,
		Platform:        in.Platform,
		Trigger:         in.Trigger,
		ScheduledFor:    in.ScheduledFor,
		WorkflowID:      in.WorkflowID,
		ExpectedResults: len(prompts),
	})
	if err != nil {
		return RunSpec{}, fmt.Errorf("upsert run: %w", err)
	}

	return RunSpec{
		TenantID: tenantID,
		RunID:    run.ID,
		Location: location,
		Prompts:  snapshots,
	}, nil
}

// FinalizeRunInput identifies the run to finalize.
type FinalizeRunInput struct {
	TenantID domain.ID
	RunID    domain.ID
}

// FinalizeRun sets the run's terminal status from its succeeded result count
// against the run's stored expected_results. It is a pure recomputation, safe
// to retry.
func (a *Activities) FinalizeRun(ctx context.Context, in FinalizeRunInput) (store.Run, error) {
	return a.Runs.FinalizeRun(ctx, in.TenantID, in.RunID)
}

// PersistProposalInput carries the generated proposal to persist as the
// business's pending proposal.
type PersistProposalInput struct {
	TenantID   domain.ID
	BusinessID domain.ID
	Payload    llm.ProposalPayload
}

// PersistProposalOutput reports the persisted (or pre-existing) proposal id.
type PersistProposalOutput struct {
	ProposalID domain.ID
}

// PersistProposal writes GenerateProfileWorkflow's output as the business's
// pending proposal. This is the only write in the generation path, and it never
// touches businesses columns (design 02/03 invariant: only apply writes profile
// values to businesses).
//
// It is idempotent against Temporal's at-least-once activity execution: if a
// prior attempt's insert committed but its ack was lost, the retry hits the
// partial-unique-index violation (ErrPendingProposalExists) and reuses that row
// instead of erroring. A missing/cross-tenant business or an empty payload is
// bad input and non-retryable.
func (a *Activities) PersistProposal(ctx context.Context, in PersistProposalInput) (PersistProposalOutput, error) {
	raw, err := json.Marshal(in.Payload)
	if err != nil {
		return PersistProposalOutput{}, temporal.NewNonRetryableApplicationError(
			"marshal proposal payload", "BadPayload", err)
	}

	proposal, err := a.Proposals.CreatePending(ctx, in.TenantID, in.BusinessID, raw)
	if err != nil {
		if errors.Is(err, store.ErrPendingProposalExists) {
			existing, getErr := a.Proposals.GetPending(ctx, in.TenantID, in.BusinessID)
			if getErr != nil {
				return PersistProposalOutput{}, getErr
			}
			return PersistProposalOutput{ProposalID: existing.ID}, nil
		}
		if errors.Is(err, store.ErrNotFound) {
			return PersistProposalOutput{}, temporal.NewNonRetryableApplicationError(
				"persist proposal for business", "BadBusinessData", err)
		}
		return PersistProposalOutput{}, err
	}
	return PersistProposalOutput{ProposalID: proposal.ID}, nil
}
