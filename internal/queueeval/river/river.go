// Package river implements the coarse-job monitoring spike. Domain state is
// the durable checkpoint: retrying a coarse job re-enters idempotent operations
// and skips prompt or analysis rows that already exist.
package river

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/queueeval"
	"opensight/internal/store"
	"opensight/internal/workflows"

	riverqueue "github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const (
	monitorQueue    = "monitoring"
	analysisQueue   = "analysis"
	assessmentQueue = "assessment"
)

// Inserter is satisfied by river.Client and keeps worker tests database-free.
type Inserter interface {
	Insert(context.Context, riverqueue.JobArgs, *riverqueue.InsertOpts) (*rivertype.JobInsertResult, error)
}

type MonitorArgs struct {
	BusinessID   domain.ID        `json:"business_id" river:"unique"`
	Platform     string           `json:"platform" river:"unique"`
	ScheduledFor time.Time        `json:"scheduled_for" river:"unique"`
	Trigger      store.RunTrigger `json:"trigger"`
}

func (MonitorArgs) Kind() string { return "opensight_monitor" }
func (MonitorArgs) InsertOpts() riverqueue.InsertOpts {
	return riverqueue.InsertOpts{
		MaxAttempts: 3,
		Queue:       monitorQueue,
		UniqueOpts:  riverqueue.UniqueOpts{ByArgs: true},
	}
}

type AnalyzeArgs struct {
	AccountID  domain.ID `json:"account_id" river:"unique"`
	BusinessID domain.ID `json:"business_id" river:"unique"`
	RunID      domain.ID `json:"run_id" river:"unique"`
}

func (AnalyzeArgs) Kind() string { return "opensight_analyze" }
func (AnalyzeArgs) InsertOpts() riverqueue.InsertOpts {
	return riverqueue.InsertOpts{
		MaxAttempts: 3,
		Queue:       analysisQueue,
		// Re-analysis must be insertable after the previous job completes, while
		// duplicate live jobs for the same run are still suppressed.
		UniqueOpts: riverqueue.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable,
			rivertype.JobStatePending,
			rivertype.JobStateRetryable,
			rivertype.JobStateRunning,
			rivertype.JobStateScheduled,
		}},
	}
}

type AssessArgs queueeval.AssessmentInput

func (AssessArgs) Kind() string { return "opensight_assess" }
func (AssessArgs) InsertOpts() riverqueue.InsertOpts {
	return riverqueue.InsertOpts{MaxAttempts: 2, Queue: assessmentQueue, UniqueOpts: riverqueue.UniqueOpts{ByArgs: true}}
}

// MonitorWorker is intentionally coarse. Prompt calls fan out inside one job;
// the prompt_results uniqueness constraint is the recovery checkpoint if the
// worker dies and River retries the job.
type MonitorWorker struct {
	riverqueue.WorkerDefaults[MonitorArgs]
	Ops         queueeval.Operations
	Jobs        Inserter
	Concurrency int
}

func (w *MonitorWorker) Timeout(*riverqueue.Job[MonitorArgs]) time.Duration { return 45 * time.Minute }

func (w *MonitorWorker) Work(ctx context.Context, job *riverqueue.Job[MonitorArgs]) error {
	if job.Args.ScheduledFor.IsZero() {
		return riverqueue.JobCancel(fmt.Errorf("scheduled_for is required"))
	}

	gate, err := w.Ops.CheckRunAccess(ctx, workflows.CheckRunAccessInput{BusinessID: job.Args.BusinessID})
	if err != nil {
		return err
	}
	if gate.Access != billing.AccessFull.String() {
		return nil
	}

	executionID := fmt.Sprintf("river-%d", job.ID)
	spec, err := w.Ops.LoadRunSpec(ctx, workflows.LoadRunSpecInput{
		BusinessID: job.Args.BusinessID, Platform: job.Args.Platform,
		ScheduledFor: job.Args.ScheduledFor, Trigger: job.Args.Trigger, WorkflowID: executionID,
	})
	if err != nil {
		return err
	}

	_, err = queueeval.ParallelMap(ctx, w.Concurrency, spec.Prompts,
		func(ctx context.Context, prompt workflows.PromptSnapshot) (workflows.ExecutePromptOutput, error) {
			return w.Ops.ExecutePrompt(ctx, workflows.ExecutePromptInput{
				AccountID: spec.AccountID, RunID: spec.RunID, Prompt: prompt, Location: spec.Location,
			})
		},
		func(prompt workflows.PromptSnapshot, err error) {
			slog.ErrorContext(ctx, "river spike: prompt failed after retries", "run_id", spec.RunID, "prompt_id", prompt.ID, "error", err)
		})
	if err != nil {
		return err
	}

	if _, err := w.Ops.FinalizeRun(ctx, workflows.FinalizeRunInput{AccountID: spec.AccountID, RunID: spec.RunID}); err != nil {
		return err
	}
	_, err = w.Jobs.Insert(ctx, AnalyzeArgs{AccountID: spec.AccountID, BusinessID: spec.BusinessID, RunID: spec.RunID}, nil)
	return err
}

type AnalyzeWorker struct {
	riverqueue.WorkerDefaults[AnalyzeArgs]
	Ops         queueeval.Operations
	Jobs        Inserter
	Concurrency int
}

func (w *AnalyzeWorker) Timeout(*riverqueue.Job[AnalyzeArgs]) time.Duration { return 30 * time.Minute }

func (w *AnalyzeWorker) Work(ctx context.Context, job *riverqueue.Job[AnalyzeArgs]) error {
	input := workflows.AnalyzeRunInput{AccountID: job.Args.AccountID, RunID: job.Args.RunID}
	spec, err := w.Ops.LoadAnalyzeRunSpec(ctx, input)
	if err != nil {
		return err
	}

	outputs, err := queueeval.ParallelMap(ctx, w.Concurrency, spec.ResultIDs,
		func(ctx context.Context, resultID domain.ID) (workflows.AnalyzeResultOutput, error) {
			return w.Ops.AnalyzeResult(ctx, workflows.AnalyzeResultInput{AccountID: job.Args.AccountID, ResultID: resultID})
		},
		func(resultID domain.ID, err error) {
			slog.ErrorContext(ctx, "river spike: result analysis failed after retries", "run_id", job.Args.RunID, "result_id", resultID, "error", err)
		})
	if err != nil {
		return err
	}

	results := make([]workflows.ResultEntities, 0, len(outputs))
	for _, output := range outputs {
		if output.Analyzed {
			results = append(results, workflows.ResultEntities{ResultID: output.ResultID, Entities: output.Entities})
		}
	}
	if _, err := w.Ops.ReconcileEntities(ctx, workflows.ReconcileEntitiesInput{
		AccountID: job.Args.AccountID, BusinessID: spec.BusinessID, RunID: job.Args.RunID, Results: results,
	}); err != nil {
		return err
	}

	_, err = w.Jobs.Insert(ctx, AssessArgs{AccountID: job.Args.AccountID, BusinessID: spec.BusinessID, RunID: job.Args.RunID}, nil)
	return err
}

type AssessWorker struct {
	riverqueue.WorkerDefaults[AssessArgs]
	Ops queueeval.Operations
}

func (w *AssessWorker) Timeout(*riverqueue.Job[AssessArgs]) time.Duration { return 30 * time.Minute }
func (w *AssessWorker) Work(ctx context.Context, job *riverqueue.Job[AssessArgs]) error {
	return w.Ops.Assess(ctx, queueeval.AssessmentInput(job.Args))
}

// AddWorkers registers the three production-shaped workers on a River bundle.
func AddWorkers(workers *riverqueue.Workers, ops queueeval.Operations, jobs Inserter, concurrency int) {
	riverqueue.AddWorker(workers, &MonitorWorker{Ops: ops, Jobs: jobs, Concurrency: concurrency})
	riverqueue.AddWorker(workers, &AnalyzeWorker{Ops: ops, Jobs: jobs, Concurrency: concurrency})
	riverqueue.AddWorker(workers, &AssessWorker{Ops: ops})
}
