package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/store"
	"opensight/internal/visibility"
	"opensight/internal/workflows"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type recordingInserter struct {
	mu   sync.Mutex
	args []river.JobArgs
}

func (r *recordingInserter) Insert(_ context.Context, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args = append(r.args, args)
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: int64(len(r.args))}}, nil
}

type stubOperations struct {
	check          func(context.Context, workflows.CheckRunAccessInput) (workflows.CheckRunAccessOutput, error)
	loadRun        func(context.Context, workflows.LoadRunSpecInput) (workflows.RunSpec, error)
	execute        func(context.Context, workflows.ExecutePromptInput) (workflows.ExecutePromptOutput, error)
	finalize       func(context.Context, workflows.FinalizeRunInput) (store.Run, error)
	load           func(context.Context, workflows.AnalyzeInput) (store.AnalysisJobSpec, error)
	analyze        func(context.Context, workflows.AnalyzeResultInput) (workflows.AnalyzeResultOutput, error)
	reconcile      func(context.Context, workflows.ReconcileEntitiesInput) (workflows.ReconcileEntitiesOutput, error)
	fetchSite      func(context.Context, workflows.FetchSiteInput) (workflows.FetchSiteOutput, error)
	proposeProfile func(context.Context, workflows.ProposeProfileInput) (workflows.ProposeProfileOutput, error)
	assessment     func(context.Context, workflows.AssessmentInput) (workflows.SiteAuditResult, error)
	find           func(context.Context, workflows.FindImprovementsInput) ([]visibility.Finding, error)
	publish        func(context.Context, workflows.PublishImproveRunInput) error
}

func (s stubOperations) CheckRunAccess(ctx context.Context, in workflows.CheckRunAccessInput) (workflows.CheckRunAccessOutput, error) {
	return s.check(ctx, in)
}
func (s stubOperations) LoadRunSpec(ctx context.Context, in workflows.LoadRunSpecInput) (workflows.RunSpec, error) {
	return s.loadRun(ctx, in)
}
func (s stubOperations) ExecutePrompt(ctx context.Context, in workflows.ExecutePromptInput) (workflows.ExecutePromptOutput, error) {
	return s.execute(ctx, in)
}
func (s stubOperations) FinalizeRun(ctx context.Context, in workflows.FinalizeRunInput) (store.Run, error) {
	return s.finalize(ctx, in)
}
func (s stubOperations) LoadAnalysisJobSpec(ctx context.Context, in workflows.AnalyzeInput) (store.AnalysisJobSpec, error) {
	return s.load(ctx, in)
}
func (s stubOperations) AnalyzeResult(ctx context.Context, in workflows.AnalyzeResultInput) (workflows.AnalyzeResultOutput, error) {
	return s.analyze(ctx, in)
}
func (s stubOperations) ReconcileEntities(ctx context.Context, in workflows.ReconcileEntitiesInput) (workflows.ReconcileEntitiesOutput, error) {
	return s.reconcile(ctx, in)
}
func (s stubOperations) FetchSite(ctx context.Context, in workflows.FetchSiteInput) (workflows.FetchSiteOutput, error) {
	return s.fetchSite(ctx, in)
}
func (s stubOperations) ProposeProfile(ctx context.Context, in workflows.ProposeProfileInput) (workflows.ProposeProfileOutput, error) {
	return s.proposeProfile(ctx, in)
}
func (s stubOperations) RunSiteAudit(ctx context.Context, in workflows.AssessmentInput) (workflows.SiteAuditResult, error) {
	return s.assessment(ctx, in)
}
func (s stubOperations) RunFinders(ctx context.Context, in workflows.FindImprovementsInput) ([]visibility.Finding, error) {
	return s.find(ctx, in)
}
func (s stubOperations) PublishImproveRun(ctx context.Context, in workflows.PublishImproveRunInput) error {
	return s.publish(ctx, in)
}

func TestMonitorWorkerFinalizesPartialPromptFailureAndEnqueuesAnalysis(t *testing.T) {
	accountID, businessID, runID := testID(1), testID(2), testID(3)
	prompts := []workflows.PromptSnapshot{{ID: testID(4)}, {ID: testID(5)}}
	finalized := false
	ops := stubOperations{
		check: func(context.Context, workflows.CheckRunAccessInput) (workflows.CheckRunAccessOutput, error) {
			return workflows.CheckRunAccessOutput{AccountID: accountID, Access: billing.AccessFull.String()}, nil
		},
		loadRun: func(context.Context, workflows.LoadRunSpecInput) (workflows.RunSpec, error) {
			return workflows.RunSpec{AccountID: accountID, BusinessID: businessID, RunID: runID, Prompts: prompts}, nil
		},
		execute: func(_ context.Context, in workflows.ExecutePromptInput) (workflows.ExecutePromptOutput, error) {
			if in.Prompt.ID == prompts[1].ID {
				return workflows.ExecutePromptOutput{}, errors.New("terminal prompt failure")
			}
			return workflows.ExecutePromptOutput{ResultID: testID(6), Status: store.ResultStatusSucceeded}, nil
		},
		finalize: func(context.Context, workflows.FinalizeRunInput) (store.Run, error) {
			finalized = true
			return store.Run{}, nil
		},
	}
	inserter := &recordingInserter{}
	worker := &MonitorWorker{Ops: ops, Jobs: inserter, Concurrency: 2}
	err := worker.Work(context.Background(), &river.Job[MonitorArgs]{JobRow: &rivertype.JobRow{ID: 91}, Args: MonitorArgs{
		BusinessID: businessID, Platform: store.PlatformChatGPT, ScheduledFor: time.Now(), Trigger: store.RunTriggerScheduled,
	}})
	if err != nil || !finalized {
		t.Fatalf("Work error/finalized = %v/%t", err, finalized)
	}
	if len(inserter.args) != 1 {
		t.Fatalf("inserted jobs = %d, want analysis", len(inserter.args))
	}
	if got, ok := inserter.args[0].(AnalyzeArgs); !ok || got.RunID != runID {
		t.Fatalf("inserted job = %#v, want analysis for %s", inserter.args[0], runID)
	}
}

func TestAnalyzeWorkerSkipsFailedExtractionAndEnqueuesAssessment(t *testing.T) {
	accountID, businessID, runID := testID(11), testID(12), testID(13)
	good, bad := testID(14), testID(15)
	attempts := 0
	reconciled := 0
	ops := stubOperations{
		load: func(context.Context, workflows.AnalyzeInput) (store.AnalysisJobSpec, error) {
			return store.AnalysisJobSpec{BusinessID: businessID, ResultIDs: []domain.ID{good, bad}}, nil
		},
		analyze: func(_ context.Context, in workflows.AnalyzeResultInput) (workflows.AnalyzeResultOutput, error) {
			if in.ResultID == bad {
				attempts++
				return workflows.AnalyzeResultOutput{}, errors.New("bad extraction")
			}
			return workflows.AnalyzeResultOutput{ResultID: good, Analyzed: true}, nil
		},
		reconcile: func(_ context.Context, in workflows.ReconcileEntitiesInput) (workflows.ReconcileEntitiesOutput, error) {
			reconciled = len(in.Results)
			return workflows.ReconcileEntitiesOutput{}, nil
		},
	}
	inserter := &recordingInserter{}
	worker := &AnalyzeWorker{Ops: ops, Jobs: inserter, Concurrency: 2, RetryDelay: func(int) time.Duration { return 0 }}
	err := worker.Work(context.Background(), &river.Job[AnalyzeArgs]{JobRow: &rivertype.JobRow{ID: 92}, Args: AnalyzeArgs{AccountID: accountID, BusinessID: businessID, RunID: runID}})
	if err != nil || attempts != 3 || reconciled != 1 {
		t.Fatalf("Work error/attempts/reconciled = %v/%d/%d", err, attempts, reconciled)
	}
	if len(inserter.args) != 1 {
		t.Fatalf("inserted jobs = %d, want assessment", len(inserter.args))
	}
	if got, ok := inserter.args[0].(AssessArgs); !ok || got.RunID != runID {
		t.Fatalf("inserted job = %#v, want assessment for %s", inserter.args[0], runID)
	}
}

func testID(last byte) domain.ID {
	var id domain.ID
	id[0], id[6], id[8], id[15] = 1, 0x70, 0x80, last
	return id
}
