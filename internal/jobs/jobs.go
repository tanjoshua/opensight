package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"
	"opensight/internal/visibility"
	"opensight/internal/workflows"

	river "github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const (
	QueueScheduler   = "scheduler"
	QueueOnboarding  = "onboarding"
	QueueMonitoring  = "monitoring"
	QueueAnalysis    = "analysis"
	QueueAssessment  = "assessment"
	SweepInterval    = 15 * time.Minute
	weeklyRunHourUTC = 2
)

type Inserter interface {
	Insert(context.Context, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

type Operations interface {
	CheckRunAccess(context.Context, workflows.CheckRunAccessInput) (workflows.CheckRunAccessOutput, error)
	LoadRunSpec(context.Context, workflows.LoadRunSpecInput) (workflows.RunSpec, error)
	ExecutePrompt(context.Context, workflows.ExecutePromptInput) (workflows.ExecutePromptOutput, error)
	FinalizeRun(context.Context, workflows.FinalizeRunInput) (store.Run, error)
	LoadAnalysisJobSpec(context.Context, workflows.AnalyzeInput) (store.AnalysisJobSpec, error)
	AnalyzeResult(context.Context, workflows.AnalyzeResultInput) (workflows.AnalyzeResultOutput, error)
	ReconcileEntities(context.Context, workflows.ReconcileEntitiesInput) (workflows.ReconcileEntitiesOutput, error)
	FetchSite(context.Context, workflows.FetchSiteInput) (workflows.FetchSiteOutput, error)
	ProposeProfile(context.Context, workflows.ProposeProfileInput) (workflows.ProposeProfileOutput, error)
	RunSiteAudit(context.Context, workflows.AssessmentInput) (workflows.SiteAuditResult, error)
	RunFinders(context.Context, workflows.FindImprovementsInput) ([]visibility.Finding, error)
	PublishImproveRun(context.Context, workflows.PublishImproveRunInput) error
}

type SweepArgs struct{}

func (SweepArgs) Kind() string { return "opensight_scheduler_sweep" }
func (SweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueScheduler, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: SweepInterval}}
}

type GenerateProfileArgs struct {
	AccountID    domain.ID `json:"account_id"`
	BusinessID   domain.ID `json:"business_id"`
	GenerationID domain.ID `json:"generation_id" river:"unique"`
	Name         string    `json:"name"`
	Website      string    `json:"website"`
}

func (GenerateProfileArgs) Kind() string { return "opensight_generate_profile" }
func (GenerateProfileArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueOnboarding, MaxAttempts: 2, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

type MonitorArgs struct {
	BusinessID   domain.ID        `json:"business_id" river:"unique"`
	Platform     string           `json:"platform" river:"unique"`
	ScheduledFor time.Time        `json:"scheduled_for" river:"unique"`
	Trigger      store.RunTrigger `json:"trigger"`
}

func (MonitorArgs) Kind() string { return "opensight_monitor" }
func (MonitorArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueMonitoring, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

type AnalyzeArgs struct{ AccountID, BusinessID, RunID domain.ID }

func (AnalyzeArgs) Kind() string { return "opensight_analyze" }
func (AnalyzeArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAnalysis, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable, rivertype.JobStateRunning, rivertype.JobStateScheduled}}}
}

type AssessArgs struct{ AccountID, BusinessID, RunID domain.ID }

func (AssessArgs) Kind() string { return "opensight_assess" }
func (AssessArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAssessment, MaxAttempts: 2, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable, rivertype.JobStateRunning, rivertype.JobStateScheduled}}}
}

func scheduleDay(id domain.ID) int {
	h := fnv.New32a()
	_, _ = h.Write(id[:])
	return int(h.Sum32() % 7)
}

// LatestDueWeeklySlot returns only the most recent due slot, and never a slot
// from before activation. This is the no-backfill startup catch-up rule.
func LatestDueWeeklySlot(id domain.ID, activatedAt, now time.Time) (time.Time, bool) {
	now = now.UTC()
	activatedAt = activatedAt.UTC()
	delta := (int(now.Weekday()) - scheduleDay(id) + 7) % 7
	slot := time.Date(now.Year(), now.Month(), now.Day(), weeklyRunHourUTC, 0, 0, 0, time.UTC).AddDate(0, 0, -delta)
	if slot.After(now) {
		slot = slot.AddDate(0, 0, -7)
	}
	return slot, !slot.Before(activatedAt)
}

func NextWeeklySlot(id domain.ID, after time.Time) time.Time {
	after = after.UTC()
	delta := (scheduleDay(id) - int(after.Weekday()) + 7) % 7
	slot := time.Date(after.Year(), after.Month(), after.Day(), weeklyRunHourUTC, 0, 0, 0, time.UTC).AddDate(0, 0, delta)
	if !slot.After(after) {
		slot = slot.AddDate(0, 0, 7)
	}
	return slot
}

type schedulerStore interface {
	ListMonitoringCandidates(context.Context) ([]store.MonitoringCandidate, error)
	MonitoringRunExists(context.Context, domain.ID, string, time.Time) (bool, error)
}

type generationStore interface {
	UpdateGenerationStage(context.Context, domain.ID, domain.ID, string) (bool, error)
	CreatePendingFenced(context.Context, domain.ID, domain.ID, domain.ID, json.RawMessage) (store.ProfileProposal, bool, error)
	FinishGeneration(context.Context, domain.ID, domain.ID, string) (bool, error)
}

type SweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	Store schedulerStore
	Jobs  Inserter
	Now   func() time.Time
}

func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}
	candidates, err := w.Store.ListMonitoringCandidates(ctx)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		plan, err := billing.PlanFor(c.PlanCode)
		if err != nil {
			return err
		}
		if !billing.DeriveAccess(c.AccessState, now).Active() {
			continue
		}
		if plan.RunInterval != "weekly" {
			return fmt.Errorf("unsupported run interval %q", plan.RunInterval)
		}
		slot, ok := LatestDueWeeklySlot(c.BusinessID, c.ActivatedAt, now)
		if !ok {
			continue
		}
		scheduledFor := time.Date(slot.Year(), slot.Month(), slot.Day(), 0, 0, 0, 0, time.UTC)
		for _, platform := range plan.Platforms {
			exists, err := w.Store.MonitoringRunExists(ctx, c.BusinessID, platform, scheduledFor)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			if _, err = w.Jobs.Insert(ctx, MonitorArgs{BusinessID: c.BusinessID, Platform: platform, ScheduledFor: scheduledFor, Trigger: store.RunTriggerScheduled}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

type GenerateProfileWorker struct {
	river.WorkerDefaults[GenerateProfileArgs]
	Store generationStore
	Ops   Operations
}

func (w *GenerateProfileWorker) Timeout(*river.Job[GenerateProfileArgs]) time.Duration {
	return 20 * time.Minute
}
func (w *GenerateProfileWorker) Work(ctx context.Context, job *river.Job[GenerateProfileArgs]) error {
	in := job.Args
	fetch, fetchErr := w.Ops.FetchSite(ctx, workflows.FetchSiteInput{Website: in.Website})
	current, err := w.Store.UpdateGenerationStage(ctx, in.BusinessID, in.GenerationID, store.GenerationStageDrafting)
	if err != nil {
		return err
	}
	if !current {
		return nil
	}
	siteText := ""
	if fetchErr == nil {
		siteText = fetch.Text
	}
	proposed, err := w.Ops.ProposeProfile(ctx, workflows.ProposeProfileInput{Name: in.Name, Website: in.Website, SiteText: siteText, Location: llm.Location{Country: "SG"}})
	if err != nil || !proposed.Proposed {
		if cause := context.Cause(ctx); errors.Is(cause, river.ErrJobCancelledRemotely) {
			return cause
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return context.Cause(ctx)
		}
		if job.Attempt >= job.MaxAttempts || workflows.IsPermanent(err) {
			if _, finishErr := w.Store.FinishGeneration(context.WithoutCancel(ctx), in.BusinessID, in.GenerationID, store.GenerationStatusFailed); finishErr != nil {
				return fmt.Errorf("finish failed profile generation: %w", finishErr)
			}
			return nil
		}
		if err == nil {
			err = errors.New("proposal failed validation")
		}
		return err
	}
	payload := proposed.Payload
	if fetchErr != nil && !proposed.OpenedOwnSite {
		payload.LowConfidence = true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return river.JobCancel(err)
	}
	_, written, err := w.Store.CreatePendingFenced(ctx, in.AccountID, in.BusinessID, in.GenerationID, raw)
	if errors.Is(err, store.ErrPendingProposalExists) {
		// A retry may resume after the proposal committed but before generation
		// state reached ready. The generation fence was checked in the failed
		// transaction, so completing the current token is safe.
		written, err = true, nil
	}
	if err != nil {
		return err
	}
	if !written {
		return nil
	}
	_, err = w.Store.FinishGeneration(ctx, in.BusinessID, in.GenerationID, store.GenerationStatusReady)
	return err
}

type MonitorWorker struct {
	river.WorkerDefaults[MonitorArgs]
	Ops         Operations
	Jobs        Inserter
	Concurrency int
}

func (w *MonitorWorker) Timeout(*river.Job[MonitorArgs]) time.Duration { return 45 * time.Minute }
func (w *MonitorWorker) Work(ctx context.Context, job *river.Job[MonitorArgs]) error {
	if job.Args.ScheduledFor.IsZero() {
		return river.JobCancel(errors.New("scheduled_for is required"))
	}
	gate, err := w.Ops.CheckRunAccess(ctx, workflows.CheckRunAccessInput{BusinessID: job.Args.BusinessID})
	if err != nil {
		return jobError(err)
	}
	if gate.Access != billing.AccessFull.String() {
		return nil
	}
	spec, err := w.Ops.LoadRunSpec(ctx, workflows.LoadRunSpecInput{BusinessID: job.Args.BusinessID, Platform: job.Args.Platform, ScheduledFor: job.Args.ScheduledFor, Trigger: job.Args.Trigger, JobID: job.ID})
	if err != nil {
		return jobError(err)
	}
	var promptErrs []error
	var promptErrsMu sync.Mutex
	_, err = parallelMap(ctx, w.Concurrency, spec.Prompts, func(ctx context.Context, p workflows.PromptSnapshot) (workflows.ExecutePromptOutput, error) {
		return w.Ops.ExecutePrompt(ctx, workflows.ExecutePromptInput{AccountID: spec.AccountID, RunID: spec.RunID, Prompt: p, Location: spec.Location})
	}, func(p workflows.PromptSnapshot, err error) {
		slog.ErrorContext(ctx, "prompt execution failed", "run_id", spec.RunID, "prompt_id", p.ID, "error", err)
		promptErrsMu.Lock()
		promptErrs = append(promptErrs, fmt.Errorf("execute prompt %s: %w", p.ID, err))
		promptErrsMu.Unlock()
	})
	if err != nil {
		return err
	}
	if err = errors.Join(promptErrs...); err != nil {
		return jobError(err)
	}
	if _, err = w.Ops.FinalizeRun(ctx, workflows.FinalizeRunInput{AccountID: spec.AccountID, RunID: spec.RunID}); err != nil {
		return err
	}
	_, err = w.Jobs.Insert(ctx, AnalyzeArgs{AccountID: spec.AccountID, BusinessID: spec.BusinessID, RunID: spec.RunID}, nil)
	return err
}

type AnalyzeWorker struct {
	river.WorkerDefaults[AnalyzeArgs]
	Ops         Operations
	Jobs        Inserter
	Concurrency int
	RetryDelay  func(int) time.Duration
}

func (w *AnalyzeWorker) Timeout(*river.Job[AnalyzeArgs]) time.Duration { return 30 * time.Minute }
func (w *AnalyzeWorker) Work(ctx context.Context, job *river.Job[AnalyzeArgs]) error {
	spec, err := w.Ops.LoadAnalysisJobSpec(ctx, workflows.AnalyzeInput{AccountID: job.Args.AccountID, RunID: job.Args.RunID})
	if err != nil {
		return jobError(err)
	}
	outs, err := parallelMap(ctx, w.Concurrency, spec.ResultIDs, func(ctx context.Context, id domain.ID) (out workflows.AnalyzeResultOutput, err error) {
		for n := 0; n < 3; n++ {
			out, err = w.Ops.AnalyzeResult(ctx, workflows.AnalyzeResultInput{AccountID: job.Args.AccountID, ResultID: id})
			if err == nil {
				return out, nil
			}
			if n < 2 && !wait(ctx, w.retryDelay(n)) {
				return out, ctx.Err()
			}
		}
		return out, err
	}, func(id domain.ID, err error) {
		slog.ErrorContext(ctx, "result analysis failed", "run_id", job.Args.RunID, "result_id", id, "error", err)
	})
	if err != nil {
		return jobError(err)
	}
	results := make([]workflows.ResultEntities, 0, len(outs))
	for _, o := range outs {
		if o.Analyzed {
			results = append(results, workflows.ResultEntities{ResultID: o.ResultID, Entities: o.Entities})
		}
	}
	for n := 0; n < 3; n++ {
		_, err = w.Ops.ReconcileEntities(ctx, workflows.ReconcileEntitiesInput{AccountID: job.Args.AccountID, BusinessID: spec.BusinessID, RunID: job.Args.RunID, Results: results})
		if err == nil {
			break
		}
		if n < 2 && !wait(ctx, w.retryDelay(n)) {
			return ctx.Err()
		}
	}
	if err != nil {
		return err
	}
	_, err = w.Jobs.Insert(ctx, AssessArgs{AccountID: job.Args.AccountID, BusinessID: job.Args.BusinessID, RunID: job.Args.RunID}, nil)
	return err
}

func (w *AnalyzeWorker) retryDelay(attempt int) time.Duration {
	if w.RetryDelay != nil {
		return w.RetryDelay(attempt)
	}
	return time.Duration(1<<attempt) * time.Second
}

type AssessWorker struct {
	river.WorkerDefaults[AssessArgs]
	Ops Operations
}

func (w *AssessWorker) Timeout(*river.Job[AssessArgs]) time.Duration { return 30 * time.Minute }
func (w *AssessWorker) Work(ctx context.Context, job *river.Job[AssessArgs]) error {
	in := workflows.AssessmentInput{AccountID: job.Args.AccountID, BusinessID: job.Args.BusinessID, RunID: job.Args.RunID}
	audit, err := w.Ops.RunSiteAudit(ctx, in)
	if err != nil {
		return jobError(err)
	}
	findings, err := w.Ops.RunFinders(ctx, workflows.FindImprovementsInput{AssessmentInput: in, Audit: audit})
	if err != nil {
		return jobError(err)
	}
	return jobError(w.Ops.PublishImproveRun(ctx, workflows.PublishImproveRunInput{AssessmentInput: in, Audit: audit, Findings: findings}))
}

func jobError(err error) error {
	if workflows.IsPermanent(err) {
		return river.JobCancel(err)
	}
	return err
}

func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func AddWorkers(workers *river.Workers, st *store.Store, ops Operations, inserter Inserter, concurrency int) {
	river.AddWorker(workers, &SweepWorker{Store: st, Jobs: inserter})
	river.AddWorker(workers, &GenerateProfileWorker{Store: st, Ops: ops})
	river.AddWorker(workers, &MonitorWorker{Ops: ops, Jobs: inserter, Concurrency: concurrency})
	river.AddWorker(workers, &AnalyzeWorker{Ops: ops, Jobs: inserter, Concurrency: concurrency})
	river.AddWorker(workers, &AssessWorker{Ops: ops})
}
