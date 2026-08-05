package workflows

import (
	"context"
	"errors"
	"time"

	"opensight/internal/domain"
	"opensight/internal/store"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// MaxAnalyzeResultAttempts and MaxReconcileAttempts are AnalyzeRun's own retry
// budgets, independent of RunWorkflow's ExecutePrompt policy — a failed analysis
// must never re-spend prompt executions.
const (
	MaxAnalyzeResultAttempts = 3
	MaxReconcileAttempts     = 3
)

// AnalyzeRunInput points AnalyzeRun at one run. Started as a child workflow by
// RunWorkflow, or manually via the Temporal CLI/UI to re-analyze an old run
// (ReanalyzeRun is this same workflow — design 05).
type AnalyzeRunInput struct {
	AccountID domain.ID `json:"TenantID"`
	RunID     domain.ID
}

// LoadAnalyzeRunSpec resolves a run's business and succeeded result ids for the
// AnalyzeRun fan-out. A missing or cross-account run is non-retryable: it
// will not fix itself, and analysis of a bad run id should fail fast.
func (a *Activities) LoadAnalyzeRunSpec(ctx context.Context, in AnalyzeRunInput) (store.AnalyzeRunSpec, error) {
	spec, err := a.Store.LoadAnalyzeRunSpec(ctx, in.AccountID, in.RunID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.AnalyzeRunSpec{}, temporal.NewNonRetryableApplicationError(
				"load analyze run spec", "BadRun", err)
		}
		return store.AnalyzeRunSpec{}, err
	}
	return spec, nil
}

// AnalyzeRun is the analysis child workflow (design 05): fan out one
// AnalyzeResult per succeeded result, then one serial ReconcileEntities over the
// analyzed ones. A single AnalyzeResult failure is logged and skipped (that
// result simply gets no result_analyses row and is excluded from metrics) — it
// never aborts the run, matching ExecutePrompt's per-prompt posture in
// RunWorkflow. AnalyzeResult and ReconcileEntities each carry their own retry
// policy, separate from prompt execution's.
func AnalyzeRun(ctx workflow.Context, input AnalyzeRunInput) error {
	loadCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	})
	var spec store.AnalyzeRunSpec
	if err := workflow.ExecuteActivity(loadCtx, acts.LoadAnalyzeRunSpec, input).Get(ctx, &spec); err != nil {
		return err
	}

	analyzeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 10 * time.Second,
			MaximumAttempts: MaxAnalyzeResultAttempts,
		},
	})

	// Fan out all extractions unthrottled; worker activity slots cap concurrency.
	futures := make([]workflow.Future, 0, len(spec.ResultIDs))
	for _, resultID := range spec.ResultIDs {
		futures = append(futures, workflow.ExecuteActivity(analyzeCtx, acts.AnalyzeResult, AnalyzeResultInput{
			AccountID: input.AccountID,
			ResultID:  resultID,
		}))
	}

	results := make([]ResultEntities, 0, len(futures))
	for _, f := range futures {
		var out AnalyzeResultOutput
		if err := f.Get(ctx, &out); err != nil {
			// A single extraction failure never aborts the run; the result just
			// gets no result_analyses row and is excluded from metrics.
			workflow.GetLogger(ctx).Error("analyze result failed",
				"run_id", input.RunID.String(), "error", err.Error())
			continue
		}
		if !out.Analyzed {
			continue
		}
		results = append(results, ResultEntities{ResultID: out.ResultID, Entities: out.Entities})
	}

	reconcileCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 10 * time.Second,
			MaximumAttempts: MaxReconcileAttempts,
		},
	})
	var out ReconcileEntitiesOutput
	return workflow.ExecuteActivity(reconcileCtx, acts.ReconcileEntities, ReconcileEntitiesInput{
		AccountID:  input.AccountID,
		BusinessID: spec.BusinessID,
		RunID:      input.RunID,
		Results:    results,
	}).Get(ctx, &out)
}
