// Package queueevaltest provides the shared fake used to execute both queue
// spikes against identical monitoring behavior.
package queueevaltest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/queueeval"
	"opensight/internal/store"
	"opensight/internal/workflows"
)

type Operations struct {
	AccountID, BusinessID, RunID domain.ID
	Prompts                      []workflows.PromptSnapshot
	ResultIDs                    []domain.ID
	FailPrompt                   domain.ID
	FailAnalysis                 domain.ID
	WorkDelay                    time.Duration

	activePrompts atomic.Int32
	maxPrompts    atomic.Int32

	mu              sync.Mutex
	Finalized       bool
	Reconciled      bool
	Assessed        bool
	ReconcileInputs []workflows.ResultEntities
}

func (o *Operations) CheckRunAccess(context.Context, workflows.CheckRunAccessInput) (workflows.CheckRunAccessOutput, error) {
	return workflows.CheckRunAccessOutput{AccountID: o.AccountID, Access: billing.AccessFull.String()}, nil
}

func (o *Operations) LoadRunSpec(context.Context, workflows.LoadRunSpecInput) (workflows.RunSpec, error) {
	return workflows.RunSpec{AccountID: o.AccountID, BusinessID: o.BusinessID, RunID: o.RunID, Prompts: o.Prompts}, nil
}

func (o *Operations) ExecutePrompt(ctx context.Context, in workflows.ExecutePromptInput) (workflows.ExecutePromptOutput, error) {
	active := o.activePrompts.Add(1)
	defer o.activePrompts.Add(-1)
	for {
		maximum := o.maxPrompts.Load()
		if active <= maximum || o.maxPrompts.CompareAndSwap(maximum, active) {
			break
		}
	}
	if o.WorkDelay > 0 {
		select {
		case <-ctx.Done():
			return workflows.ExecutePromptOutput{}, ctx.Err()
		case <-time.After(o.WorkDelay):
		}
	}
	if in.Prompt.ID == o.FailPrompt {
		return workflows.ExecutePromptOutput{}, errors.New("prompt failed")
	}
	return workflows.ExecutePromptOutput{}, nil
}

func (o *Operations) FinalizeRun(context.Context, workflows.FinalizeRunInput) (store.Run, error) {
	o.mu.Lock()
	o.Finalized = true
	o.mu.Unlock()
	return store.Run{ID: o.RunID, BusinessID: o.BusinessID}, nil
}

func (o *Operations) LoadAnalyzeRunSpec(context.Context, workflows.AnalyzeRunInput) (store.AnalyzeRunSpec, error) {
	return store.AnalyzeRunSpec{BusinessID: o.BusinessID, ResultIDs: o.ResultIDs}, nil
}

func (o *Operations) AnalyzeResult(_ context.Context, in workflows.AnalyzeResultInput) (workflows.AnalyzeResultOutput, error) {
	if in.ResultID == o.FailAnalysis {
		return workflows.AnalyzeResultOutput{}, errors.New("analysis failed")
	}
	return workflows.AnalyzeResultOutput{ResultID: in.ResultID, Analyzed: true}, nil
}

func (o *Operations) ReconcileEntities(_ context.Context, in workflows.ReconcileEntitiesInput) (workflows.ReconcileEntitiesOutput, error) {
	o.mu.Lock()
	o.Reconciled = true
	o.ReconcileInputs = append([]workflows.ResultEntities(nil), in.Results...)
	o.mu.Unlock()
	return workflows.ReconcileEntitiesOutput{}, nil
}

func (o *Operations) Assess(context.Context, queueeval.AssessmentInput) error {
	o.mu.Lock()
	o.Assessed = true
	o.mu.Unlock()
	return nil
}

func (o *Operations) MaxPromptConcurrency() int { return int(o.maxPrompts.Load()) }
