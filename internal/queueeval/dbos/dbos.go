// Package dbos implements the durable-workflow monitoring spike. Unlike the
// River version, each provider call remains a durable child workflow and each
// database boundary remains a durable step.
package dbos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/queueeval"
	"opensight/internal/store"
	"opensight/internal/workflows"

	dbosgo "github.com/dbos-inc/dbos-transact-golang/dbos"
)

const (
	monitorQueue    = "monitoring"
	promptQueue     = "prompts"
	analysisQueue   = "analysis"
	extractionQueue = "extractions"
	assessmentQueue = "assessments"
)

type MonitorInput struct {
	BusinessID   domain.ID        `json:"business_id"`
	Platform     string           `json:"platform"`
	ScheduledFor time.Time        `json:"scheduled_for"`
	Trigger      store.RunTrigger `json:"trigger"`
}

type MonitorResult struct {
	Skipped        bool `json:"skipped"`
	AnalysisFailed bool `json:"analysis_failed"`
}

type PromptInput struct {
	AccountID domain.ID                `json:"account_id"`
	RunID     domain.ID                `json:"run_id"`
	Prompt    workflows.PromptSnapshot `json:"prompt"`
	Location  llm.Location             `json:"location"`
}

type AnalyzeInput struct {
	AccountID  domain.ID `json:"account_id"`
	BusinessID domain.ID `json:"business_id"`
	RunID      domain.ID `json:"run_id"`
}

type AnalyzeResultInput struct {
	AccountID domain.ID `json:"account_id"`
	ResultID  domain.ID `json:"result_id"`
}

// Runtime is a configured DBOS workflow instance. ConfigName is persisted in
// workflow history and must stay stable across deployments.
type Runtime struct {
	Ops  queueeval.Operations
	Name string
}

func (r *Runtime) ConfigName() string {
	if r.Name == "" {
		return "opensight-monitoring"
	}
	return r.Name
}

// Register installs every workflow method before the DBOS context launches.
func (r *Runtime) Register(ctx dbosgo.DBOSContext) {
	dbosgo.RegisterWorkflow(ctx, r.Monitor, dbosgo.WithInstance(r), dbosgo.WithWorkflowName("monitor"))
	dbosgo.RegisterWorkflow(ctx, r.Prompt, dbosgo.WithInstance(r), dbosgo.WithWorkflowName("prompt"))
	dbosgo.RegisterWorkflow(ctx, r.Analyze, dbosgo.WithInstance(r), dbosgo.WithWorkflowName("analyze"))
	dbosgo.RegisterWorkflow(ctx, r.AnalyzeResult, dbosgo.WithInstance(r), dbosgo.WithWorkflowName("analyze-result"))
	dbosgo.RegisterWorkflow(ctx, r.Assess, dbosgo.WithInstance(r), dbosgo.WithWorkflowName("assess"))
}

// RegisterQueues persists the flow-control settings DBOS provides in its open
// source runtime. Prompt concurrency is global so multiple worker processes do
// not multiply provider pressure.
func RegisterQueues(ctx dbosgo.DBOSContext, concurrency int) error {
	if concurrency < 1 {
		concurrency = 1
	}
	for name, options := range map[string][]dbosgo.QueueOption{
		monitorQueue:    {dbosgo.WithWorkerConcurrency(concurrency)},
		promptQueue:     {dbosgo.WithGlobalConcurrency(concurrency)},
		analysisQueue:   {dbosgo.WithWorkerConcurrency(concurrency)},
		extractionQueue: {dbosgo.WithGlobalConcurrency(concurrency)},
		assessmentQueue: {dbosgo.WithWorkerConcurrency(concurrency)},
	} {
		if _, err := dbosgo.RegisterQueue(ctx, name, options...); err != nil {
			return fmt.Errorf("register DBOS queue %s: %w", name, err)
		}
	}
	return nil
}

func (r *Runtime) Monitor(ctx dbosgo.DBOSContext, input MonitorInput) (MonitorResult, error) {
	if input.ScheduledFor.IsZero() {
		return MonitorResult{}, errors.New("scheduled_for is required")
	}

	gate, err := dbosgo.RunAsStep(ctx, func(stepCtx context.Context) (workflows.CheckRunAccessOutput, error) {
		return r.Ops.CheckRunAccess(stepCtx, workflows.CheckRunAccessInput{BusinessID: input.BusinessID})
	}, dbosgo.WithStepName("check-run-access"), dbosgo.WithStepMaxRetries(2))
	if err != nil {
		return MonitorResult{}, err
	}
	if gate.Access != billing.AccessFull.String() {
		return MonitorResult{Skipped: true}, nil
	}

	workflowID, err := dbosgo.GetWorkflowID(ctx)
	if err != nil {
		return MonitorResult{}, err
	}
	spec, err := dbosgo.RunAsStep(ctx, func(stepCtx context.Context) (workflows.RunSpec, error) {
		return r.Ops.LoadRunSpec(stepCtx, workflows.LoadRunSpecInput{
			BusinessID: input.BusinessID, Platform: input.Platform, ScheduledFor: input.ScheduledFor,
			Trigger: input.Trigger, WorkflowID: workflowID,
		})
	}, dbosgo.WithStepName("load-run-spec"), dbosgo.WithStepMaxRetries(2))
	if err != nil {
		return MonitorResult{}, err
	}

	prompts := make([]dbosgo.WorkflowHandle[workflows.ExecutePromptOutput], 0, len(spec.Prompts))
	for _, prompt := range spec.Prompts {
		handle, err := dbosgo.RunWorkflow(ctx, r.Prompt, PromptInput{
			AccountID: spec.AccountID, RunID: spec.RunID, Prompt: prompt, Location: spec.Location,
		}, dbosgo.WithQueue(promptQueue), dbosgo.WithRunInstance(r),
			dbosgo.WithWorkflowID(fmt.Sprintf("prompt-%s-%s", spec.RunID, prompt.ID)))
		if err != nil {
			return MonitorResult{}, err
		}
		prompts = append(prompts, handle)
	}
	// All children have been submitted before waiting, so the queue executes them
	// concurrently. A terminal child failure is intentionally skipped; finalize
	// derives completed/partial/failed from the rows that actually exist.
	for _, handle := range prompts {
		_, _ = handle.GetResult()
	}

	if _, err := dbosgo.RunAsStep(ctx, func(stepCtx context.Context) (store.Run, error) {
		return r.Ops.FinalizeRun(stepCtx, workflows.FinalizeRunInput{AccountID: spec.AccountID, RunID: spec.RunID})
	}, dbosgo.WithStepName("finalize-run"), dbosgo.WithStepMaxRetries(2)); err != nil {
		return MonitorResult{}, err
	}

	analysis, err := dbosgo.RunWorkflow(ctx, r.Analyze, AnalyzeInput{
		AccountID: spec.AccountID, BusinessID: spec.BusinessID, RunID: spec.RunID,
	}, dbosgo.WithQueue(analysisQueue), dbosgo.WithRunInstance(r),
		dbosgo.WithWorkflowID(fmt.Sprintf("analyze-%s", spec.RunID)))
	if err != nil {
		return MonitorResult{}, err
	}
	if _, err := analysis.GetResult(); err != nil {
		return MonitorResult{AnalysisFailed: true}, nil
	}
	return MonitorResult{}, nil
}

func (r *Runtime) Prompt(ctx dbosgo.DBOSContext, input PromptInput) (workflows.ExecutePromptOutput, error) {
	return dbosgo.RunAsStep(ctx, func(stepCtx context.Context) (workflows.ExecutePromptOutput, error) {
		return r.Ops.ExecutePrompt(stepCtx, workflows.ExecutePromptInput{
			AccountID: input.AccountID, RunID: input.RunID, Prompt: input.Prompt, Location: input.Location,
		})
	}, dbosgo.WithStepName("execute-prompt"), dbosgo.WithStepMaxRetries(3), dbosgo.WithBaseInterval(10*time.Second))
}

func (r *Runtime) Analyze(ctx dbosgo.DBOSContext, input AnalyzeInput) (bool, error) {
	analysisWorkflowID, err := dbosgo.GetWorkflowID(ctx)
	if err != nil {
		return false, err
	}
	spec, err := dbosgo.RunAsStep(ctx, func(stepCtx context.Context) (store.AnalyzeRunSpec, error) {
		return r.Ops.LoadAnalyzeRunSpec(stepCtx, workflows.AnalyzeRunInput{AccountID: input.AccountID, RunID: input.RunID})
	}, dbosgo.WithStepName("load-analysis-spec"), dbosgo.WithStepMaxRetries(2))
	if err != nil {
		return false, err
	}

	handles := make([]dbosgo.WorkflowHandle[workflows.AnalyzeResultOutput], 0, len(spec.ResultIDs))
	for _, resultID := range spec.ResultIDs {
		handle, err := dbosgo.RunWorkflow(ctx, r.AnalyzeResult, AnalyzeResultInput{AccountID: input.AccountID, ResultID: resultID},
			// Coordinators and children need separate queues. If they shared a
			// concurrency-limited queue, waiting parents could occupy every slot and
			// prevent their children from ever starting.
			dbosgo.WithQueue(extractionQueue), dbosgo.WithRunInstance(r),
			// Scope children to this analysis execution. A manual re-analysis uses a
			// fresh parent ID and must not replay the first extraction's children.
			dbosgo.WithWorkflowID(fmt.Sprintf("analyze-result-%s-%s", analysisWorkflowID, resultID)))
		if err != nil {
			return false, err
		}
		handles = append(handles, handle)
	}

	results := make([]workflows.ResultEntities, 0, len(handles))
	for _, handle := range handles {
		output, err := handle.GetResult()
		if err == nil && output.Analyzed {
			results = append(results, workflows.ResultEntities{ResultID: output.ResultID, Entities: output.Entities})
		}
	}
	if _, err := dbosgo.RunAsStep(ctx, func(stepCtx context.Context) (workflows.ReconcileEntitiesOutput, error) {
		return r.Ops.ReconcileEntities(stepCtx, workflows.ReconcileEntitiesInput{
			AccountID: input.AccountID, BusinessID: spec.BusinessID, RunID: input.RunID, Results: results,
		})
	}, dbosgo.WithStepName("reconcile-entities"), dbosgo.WithStepMaxRetries(2), dbosgo.WithBaseInterval(10*time.Second)); err != nil {
		return false, err
	}

	assessment, err := dbosgo.RunWorkflow(ctx, r.Assess, queueeval.AssessmentInput{
		AccountID: input.AccountID, BusinessID: spec.BusinessID, RunID: input.RunID,
	}, dbosgo.WithQueue(assessmentQueue), dbosgo.WithRunInstance(r),
		dbosgo.WithWorkflowID(fmt.Sprintf("assess-%s", input.RunID)))
	if err != nil {
		return false, err
	}
	// Assessment failure preserves the latest successful published state and does
	// not turn an otherwise successful analysis into a failure.
	_, _ = assessment.GetResult()
	return true, nil
}

func (r *Runtime) AnalyzeResult(ctx dbosgo.DBOSContext, input AnalyzeResultInput) (workflows.AnalyzeResultOutput, error) {
	return dbosgo.RunAsStep(ctx, func(stepCtx context.Context) (workflows.AnalyzeResultOutput, error) {
		return r.Ops.AnalyzeResult(stepCtx, workflows.AnalyzeResultInput{AccountID: input.AccountID, ResultID: input.ResultID})
	}, dbosgo.WithStepName("analyze-result"), dbosgo.WithStepMaxRetries(2), dbosgo.WithBaseInterval(10*time.Second))
}

func (r *Runtime) Assess(ctx dbosgo.DBOSContext, input queueeval.AssessmentInput) (bool, error) {
	_, err := dbosgo.RunAsStep(ctx, func(stepCtx context.Context) (bool, error) {
		return true, r.Ops.Assess(stepCtx, input)
	}, dbosgo.WithStepName("assess-run"), dbosgo.WithStepMaxRetries(1), dbosgo.WithBaseInterval(5*time.Second))
	return err == nil, err
}
