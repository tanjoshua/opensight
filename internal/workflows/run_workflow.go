package workflows

import (
	"fmt"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/store"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// acts is a typed-nil handle used only to resolve activity method names for
// workflow.ExecuteActivity. Temporal reads the function name off the method
// value via reflection without invoking it, so the nil receiver is never
// dereferenced; the real methods run on the instance registered with the
// worker (see cmd/opensight work).
var acts *Activities

// TruncateToDay drops the time-of-day so a moment lands cleanly on the DATE
// scheduled_for represents.
func TruncateToDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// RunWorkflowID is the deterministic workflow id (design 04): schedule fires,
// the onboarding first run, and any future manual run all converge on the same
// id per business/platform/date, so duplicate triggers are no-ops.
func RunWorkflowID(businessID domain.ID, platform string, scheduledFor time.Time) string {
	return fmt.Sprintf("run-%s-%s-%s", businessID, platform, scheduledFor.UTC().Format("2006-01-02"))
}

// RunWorkflowInput starts a monitoring run for a business on a platform for a
// scheduled date.
type RunWorkflowInput struct {
	BusinessID   domain.ID
	Platform     string
	ScheduledFor time.Time
	Trigger      store.RunTrigger
}

// RunResult reports whether the run actually ran. A skipped run has no
// monitoring_runs row by design (gate 3, design 08) — the workflow result is
// the only place the skip is otherwise visible, since nothing gets written.
type RunResult struct {
	Skipped    bool
	SkipReason string
}

// RunWorkflow is the weekly monitoring run (design 04): CheckRunAccess gates
// spend on the account's current billing access, LoadRunSpec snapshots the run
// and prompts, ExecutePrompt fans out one activity per prompt, and FinalizeRun
// sets the terminal status. A single prompt's failure never aborts the run —
// its already-succeeded siblings must survive.
func RunWorkflow(ctx workflow.Context, input RunWorkflowInput) (RunResult, error) {
	gateCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	// Gate 3 (design 08, "Enforcement: three gates"): resolve access fresh,
	// before anything that costs money or writes history. Gates 1 (RPC) and 2
	// (schedule pause) both depend on a webhook that can be delayed or
	// dropped; this is the one that recomputes on every run start, so a missed
	// webhook can never turn into spend. Access is read once, here, and never
	// rechecked below — a run already in flight when access drops is allowed
	// to finish (design 08), so this is not a bug to "fix" by re-gating later.
	var gate CheckRunAccessOutput
	if err := workflow.ExecuteActivity(gateCtx, acts.CheckRunAccess, CheckRunAccessInput{
		BusinessID: input.BusinessID,
	}).Get(ctx, &gate); err != nil {
		return RunResult{}, err
	}
	if gate.Access != billing.AccessFull.String() {
		workflow.GetLogger(ctx).Warn("run skipped: account access is not full",
			"business_id", input.BusinessID.String(), "access", gate.Access)
		return RunResult{Skipped: true, SkipReason: "billing access " + gate.Access}, nil
	}

	// A Temporal Schedule fires with static Args, so scheduled runs leave
	// ScheduledFor unset; derive the week bucket from the deterministic workflow
	// clock (the fire time). Initial/manual triggers pass an explicit date.
	scheduledFor := input.ScheduledFor
	if scheduledFor.IsZero() {
		scheduledFor = workflow.Now(ctx)
	}

	loadCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})

	var spec RunSpec
	if err := workflow.ExecuteActivity(loadCtx, acts.LoadRunSpec, LoadRunSpecInput{
		BusinessID:   input.BusinessID,
		Platform:     input.Platform,
		ScheduledFor: scheduledFor,
		Trigger:      input.Trigger,
		WorkflowID:   workflow.GetInfo(ctx).WorkflowExecution.ID,
	}).Get(ctx, &spec); err != nil {
		return RunResult{}, err
	}

	execCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 120 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 10 * time.Second,
			MaximumAttempts: MaxExecutePromptAttempts,
		},
	})

	// Fan out one activity per prompt without waiting; concurrency is capped by
	// the worker's activity-slot limit, not a workflow-level construct.
	futures := make([]workflow.Future, 0, len(spec.Prompts))
	for _, prompt := range spec.Prompts {
		futures = append(futures, workflow.ExecuteActivity(execCtx, acts.ExecutePrompt, ExecutePromptInput{
			AccountID: spec.AccountID,
			RunID:     spec.RunID,
			Prompt:    prompt,
			Location:  spec.Location,
		}))
	}
	for _, f := range futures {
		var out ExecutePromptOutput
		if err := f.Get(ctx, &out); err != nil {
			// ExecutePrompt records terminal failures itself and returns nil, so
			// an error here is unexpected (e.g. a bug or exhausted DB retries).
			// Log and continue: FinalizeRun still counts this prompt as missing.
			workflow.GetLogger(ctx).Error("execute prompt failed",
				"run_id", spec.RunID.String(), "error", err.Error())
		}
	}

	// FinalizeRun sets the run's terminal status and completed_at. It must run
	// before analysis: monitoring_runs' CHECK forbids analysis_completed_at
	// (stamped by ReconcileEntities' commit) unless completed_at is already set,
	// and the design's failure posture is that the run reaches its terminal status
	// independently of analysis — analysis merely decorates it (or fails and
	// leaves it flagged for re-analysis).
	finalizeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})
	var run store.Run
	if err := workflow.ExecuteActivity(finalizeCtx, acts.FinalizeRun, FinalizeRunInput{
		AccountID: spec.AccountID,
		RunID:     spec.RunID,
	}).Get(ctx, &run); err != nil {
		return RunResult{}, err
	}

	// AnalyzeRun runs as a child workflow with its own retry budget so a failed
	// analysis never re-spends prompt executions (design 04/05). It is an ordered
	// stage — we await it — but its failure must never fail the parent run: raw
	// results stay viewable and analysis_completed_at simply stays unset, which is
	// the "flagged for re-analysis" signal. So we catch and log its error rather
	// than returning it.
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: fmt.Sprintf("analyze-%s", spec.RunID),
	})
	analysisErr := workflow.ExecuteChildWorkflow(childCtx, AnalyzeRun, AnalyzeRunInput{
		AccountID: spec.AccountID,
		RunID:     spec.RunID,
	}).Get(ctx, nil)
	if analysisErr != nil {
		workflow.GetLogger(ctx).Error("analyze run failed; run left flagged for re-analysis",
			"run_id", spec.RunID.String(), "error", analysisErr.Error())
	}

	// This child was added after RunWorkflow was already deployed. The version
	// marker preserves replay compatibility for histories that completed after
	// AnalyzeRun without scheduling assessment generation.
	if analysisErr == nil && workflow.GetVersion(ctx, "add-assessment-generation", workflow.DefaultVersion, 1) == 1 {
		assessmentCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: fmt.Sprintf("assess-%s", spec.RunID),
		})
		if err := workflow.ExecuteChildWorkflow(assessmentCtx, AssessmentWorkflow, AssessmentWorkflowInput{
			AccountID: spec.AccountID, BusinessID: spec.BusinessID, RunID: spec.RunID,
		}).Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("assessment generation failed; latest successful opportunities preserved",
				"run_id", spec.RunID.String(), "error", err.Error())
		}
	}

	return RunResult{}, nil
}
