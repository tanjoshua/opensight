// Package queueeval contains the narrow application boundary shared by the
// River and DBOS monitoring spikes. It deliberately reuses the current
// activity inputs and outputs: the experiment is about orchestration, not a
// rewrite of the already-idempotent domain operations.
package queueeval

import (
	"context"

	"opensight/internal/domain"
	"opensight/internal/store"
	"opensight/internal/workflows"
)

// Operations is the application work needed by either monitoring prototype.
// A production migration would adapt workflows.Activities to this interface
// while removing its remaining Temporal-specific error classifications.
type Operations interface {
	CheckRunAccess(context.Context, workflows.CheckRunAccessInput) (workflows.CheckRunAccessOutput, error)
	LoadRunSpec(context.Context, workflows.LoadRunSpecInput) (workflows.RunSpec, error)
	ExecutePrompt(context.Context, workflows.ExecutePromptInput) (workflows.ExecutePromptOutput, error)
	FinalizeRun(context.Context, workflows.FinalizeRunInput) (store.Run, error)

	LoadAnalyzeRunSpec(context.Context, workflows.AnalyzeRunInput) (store.AnalyzeRunSpec, error)
	AnalyzeResult(context.Context, workflows.AnalyzeResultInput) (workflows.AnalyzeResultOutput, error)
	ReconcileEntities(context.Context, workflows.ReconcileEntitiesInput) (workflows.ReconcileEntitiesOutput, error)

	Assess(context.Context, AssessmentInput) error
}

// AssessmentInput identifies the final, independently retryable stage. The
// assessment internals are not expanded here because both candidates would
// run the existing audit -> finders -> publish sequence unchanged.
type AssessmentInput struct {
	AccountID  domain.ID `json:"account_id"`
	BusinessID domain.ID `json:"business_id"`
	RunID      domain.ID `json:"run_id"`
}
