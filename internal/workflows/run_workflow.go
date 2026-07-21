package workflows

import (
	"fmt"
	"time"

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

// RunWorkflow is the weekly monitoring run (design 04): LoadRunSpec snapshots
// the run and prompts, ExecutePrompt fans out one activity per prompt, and
// FinalizeRun sets the terminal status. A single prompt's failure never aborts
// the run — its already-succeeded siblings must survive.
func RunWorkflow(ctx workflow.Context, input RunWorkflowInput) error {
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
		return err
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
			TenantID: spec.TenantID,
			RunID:    spec.RunID,
			Prompt:   prompt,
			Location: spec.Location,
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
		TenantID:        spec.TenantID,
		RunID:           spec.RunID,
		ExpectedResults: len(spec.Prompts),
	}).Get(ctx, &run); err != nil {
		return err
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
	if err := workflow.ExecuteChildWorkflow(childCtx, AnalyzeRun, AnalyzeRunInput{
		TenantID: spec.TenantID,
		RunID:    spec.RunID,
	}).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Error("analyze run failed; run left flagged for re-analysis",
			"run_id", spec.RunID.String(), "error", err.Error())
	}

	return nil
}
