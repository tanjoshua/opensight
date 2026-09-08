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
)

// Operations holds dependencies for the idempotent application operations
// invoked by River workers. Focused callers can leave unrelated fields unset.
type Operations struct {
	Store            *store.Store
	Runner           llm.PromptRunner
	Extractor        llm.ExtractionRunner
	Matcher          llm.MatchRunner
	Proposer         llm.ProposeProfileRunner
	SourceClassifier llm.SourceClassifier
	Limiter          *llm.Limiter
}

// PromptSnapshot is one active prompt captured at run start. The monitor job
// snapshots these so a mid-run prompt replacement can't produce a half-and-half
// run (design 04).
type PromptSnapshot struct {
	ID   domain.ID
	Text string
}

// RunSpec is LoadRunSpec's output: the resolved account, the upserted run, the
// business location for web search, and the prompt snapshot to fan out over.
type RunSpec struct {
	AccountID  domain.ID `json:"TenantID"`
	BusinessID domain.ID
	RunID      domain.ID
	Location   llm.Location
	Prompts    []PromptSnapshot
}

// persistedRunSpec is the immutable execution input stored with a monitoring
// run. Identity fields remain ordinary columns and are added after read-back.
type persistedRunSpec struct {
	Location llm.Location     `json:"location"`
	Prompts  []PromptSnapshot `json:"prompts"`
}

// CheckRunAccessInput identifies the business whose account access gates the run.
type CheckRunAccessInput struct {
	BusinessID domain.ID
}

// CheckRunAccessOutput carries the resolved account and its access, so a caller
// that skips the run still has the account id for logging without a second
// lookup. Access is the string form (billing.Access.String()) rather than the
// int, so the value is legible in River job records —
// AccessNever is the zero value of the int, which would otherwise read as
// "unset" rather than "never paid". MonitoringPaused is the admin's own
// pause switch, reported alongside access because both answer the same
// question: may this run proceed?
type CheckRunAccessOutput struct {
	AccountID        domain.ID `json:"TenantID"`
	Access           string
	MonitoringPaused bool
}

// CheckRunAccess is design 08's gate 3, the authoritative spend backstop:
// The monitor worker calls this before LoadRunSpec (the only monitoring_runs writer)
// and before any prompt executes, so a account without full access costs
// nothing — no run row, no prompt, no analysis. Gates 1 (RPC) and 2 (schedule
// pause) both depend on a webhook that can be delayed or dropped; this one
// recomputes access fresh against the current time on every run start, so a
// missed webhook can never turn into spend.
//
// It reads the admin pause switch from the same fresh state. The scheduler
// sweep already skips paused businesses, so this only catches the narrow
// window where a job was enqueued (or is retrying) from before the pause —
// which is exactly when an admin most expects "pause" to mean "stop now".
func (a *Operations) CheckRunAccess(ctx context.Context, in CheckRunAccessInput) (CheckRunAccessOutput, error) {
	accountID, err := a.Store.ResolveAccountID(ctx, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return CheckRunAccessOutput{}, NewPermanentError(
				"resolve account for business", "BadBusinessData", err)
		}
		return CheckRunAccessOutput{}, err
	}

	business, err := a.Store.GetBusiness(ctx, accountID, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return CheckRunAccessOutput{}, NewPermanentError(
				"load business", "BadBusinessData", err)
		}
		return CheckRunAccessOutput{}, err
	}
	sub, err := a.Store.GetByAccount(ctx, accountID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Every account is supposed to have exactly one subscriptions row
			// (design 08); a miss is a bug, not a normal skip. Non-retryable so
			// it surfaces rather than retrying forever, and it still spends
			// nothing.
			return CheckRunAccessOutput{}, NewPermanentError(
				"load subscription for account", "BadBillingData", err)
		}
		return CheckRunAccessOutput{}, err
	}

	access := billing.DeriveAccess(sub.AccessState(), time.Now().UTC())
	return CheckRunAccessOutput{AccountID: accountID, Access: access.String(),
		MonitoringPaused: business.MonitoringPausedAt != nil}, nil
}

// LoadRunSpecInput identifies the run to load or create.
type LoadRunSpecInput struct {
	BusinessID   domain.ID
	Platform     string
	ScheduledFor time.Time
	Trigger      store.RunTrigger
	JobID        int64
}

// LoadRunSpec resolves the account, validates business data, snapshots active
// prompts, and idempotently upserts the run row (design 04). Duplicate triggers
// for the same (business, platform, scheduled_for) converge on the existing run.
//
// Bad business data (missing/cross-account business, invalid location) is
// non-retryable: it won't fix itself on retry, and failing before UpsertRun
// avoids leaving a stuck running row for a data problem. Transient DB errors
// return the plain error so River retries.
func (a *Operations) LoadRunSpec(ctx context.Context, in LoadRunSpecInput) (RunSpec, error) {
	accountID, err := a.Store.ResolveAccountID(ctx, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return RunSpec{}, NewPermanentError(
				"resolve account for business", "BadBusinessData", err)
		}
		return RunSpec{}, err
	}

	// Retries restore their immutable input before consulting the mutable
	// business profile or active prompt set. The upsert below still handles the
	// race where two first attempts observe no run concurrently.
	existing, err := a.Store.GetRunByKey(ctx, accountID, in.BusinessID, in.Platform, in.ScheduledFor)
	if err == nil {
		return decodeRunSpec(accountID, in.BusinessID, existing)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return RunSpec{}, err
	}

	business, err := a.Store.GetBusiness(ctx, accountID, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return RunSpec{}, NewPermanentError(
				"load business", "BadBusinessData", err)
		}
		return RunSpec{}, err
	}

	location, err := llm.LocationFromBusinessJSON(business.Location)
	if err != nil {
		// An invalid or missing location is a profile error, not a transient
		// failure — fail before creating the run row.
		return RunSpec{}, NewPermanentError(
			"invalid business location", "BadBusinessData", err)
	}

	prompts, err := a.Store.ListActivePrompts(ctx, accountID, in.BusinessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return RunSpec{}, NewPermanentError(
				"list active prompts", "BadBusinessData", err)
		}
		return RunSpec{}, err
	}

	snapshots := make([]PromptSnapshot, 0, len(prompts))
	for _, p := range prompts {
		snapshots = append(snapshots, PromptSnapshot{ID: p.ID, Text: p.Text})
	}
	snapshotJSON, err := json.Marshal(persistedRunSpec{Location: location, Prompts: snapshots})
	if err != nil {
		return RunSpec{}, fmt.Errorf("marshal run spec: %w", err)
	}

	run, err := a.Store.UpsertRun(ctx, accountID, store.UpsertRunParams{
		BusinessID:      in.BusinessID,
		Platform:        in.Platform,
		Trigger:         in.Trigger,
		ScheduledFor:    in.ScheduledFor,
		JobID:           in.JobID,
		ExpectedResults: len(prompts),
		Spec:            snapshotJSON,
	})
	if err != nil {
		return RunSpec{}, fmt.Errorf("upsert run: %w", err)
	}

	return decodeRunSpec(accountID, in.BusinessID, run)
}

func decodeRunSpec(accountID, businessID domain.ID, run store.Run) (RunSpec, error) {
	var persisted persistedRunSpec
	if err := json.Unmarshal(run.Spec, &persisted); err != nil {
		return RunSpec{}, fmt.Errorf("decode stored run spec: %w", err)
	}
	return RunSpec{
		AccountID:  accountID,
		BusinessID: businessID,
		RunID:      run.ID,
		Location:   persisted.Location,
		Prompts:    persisted.Prompts,
	}, nil
}

// FinalizeRunInput identifies the run to finalize.
type FinalizeRunInput struct {
	AccountID domain.ID `json:"TenantID"`
	RunID     domain.ID
}

// FinalizeRun sets the run's terminal status from its succeeded result count
// against the run's stored expected_results. It is a pure recomputation, safe
// to retry.
func (a *Operations) FinalizeRun(ctx context.Context, in FinalizeRunInput) (store.Run, error) {
	return a.Store.FinalizeRun(ctx, in.AccountID, in.RunID)
}

// PersistProposalInput carries the generated proposal to persist as the
// business's pending proposal.
type PersistProposalInput struct {
	AccountID  domain.ID `json:"TenantID"`
	BusinessID domain.ID
	Payload    llm.ProposalPayload
}

// PersistProposalOutput reports the persisted (or pre-existing) proposal id.
type PersistProposalOutput struct {
	ProposalID domain.ID
}

// PersistProposal writes generated profile output as the business's
// pending proposal. This is the only write in the generation path, and it never
// touches businesses columns (design 02/03 invariant: only apply writes profile
// values to businesses).
//
// It is idempotent against River's at-least-once job execution: if a
// prior attempt's insert committed but its ack was lost, the retry hits the
// partial-unique-index violation (ErrPendingProposalExists) and reuses that row
// instead of erroring. A missing/cross-account business or an empty payload is
// bad input and non-retryable.
func (a *Operations) PersistProposal(ctx context.Context, in PersistProposalInput) (PersistProposalOutput, error) {
	raw, err := json.Marshal(in.Payload)
	if err != nil {
		return PersistProposalOutput{}, NewPermanentError(
			"marshal proposal payload", "BadPayload", err)
	}

	proposal, err := a.Store.CreatePending(ctx, in.AccountID, in.BusinessID, raw)
	if err != nil {
		if errors.Is(err, store.ErrPendingProposalExists) {
			existing, getErr := a.Store.GetPending(ctx, in.AccountID, in.BusinessID)
			if getErr != nil {
				return PersistProposalOutput{}, getErr
			}
			return PersistProposalOutput{ProposalID: existing.ID}, nil
		}
		if errors.Is(err, store.ErrNotFound) {
			return PersistProposalOutput{}, NewPermanentError(
				"persist proposal for business", "BadBusinessData", err)
		}
		return PersistProposalOutput{}, err
	}
	return PersistProposalOutput{ProposalID: proposal.ID}, nil
}
