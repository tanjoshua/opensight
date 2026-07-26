package workflows

import (
	"errors"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/store"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

func mustID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	return id
}

func specWithPrompts(t *testing.T, n int) RunSpec {
	t.Helper()
	prompts := make([]PromptSnapshot, 0, n)
	for i := 0; i < n; i++ {
		prompts = append(prompts, PromptSnapshot{ID: mustID(t), Text: "prompt"})
	}
	return RunSpec{TenantID: mustID(t), RunID: mustID(t), Prompts: prompts}
}

func runInput(t *testing.T) RunWorkflowInput {
	t.Helper()
	return RunWorkflowInput{
		BusinessID:   mustID(t),
		Platform:     store.PlatformChatGPT,
		ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
		Trigger:      store.RunTriggerScheduled,
	}
}

// TestRunWorkflowFansOutAndFinalizes covers the happy path: N prompts produce N
// ExecutePrompt calls and one FinalizeRun call for the run.
func TestRunWorkflowFansOutAndFinalizes(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities
	const n = 3
	spec := specWithPrompts(t, n)

	env.OnActivity(a.LoadRunSpec, mock.Anything, mock.Anything).Return(spec, nil).Once()
	env.OnActivity(a.ExecutePrompt, mock.Anything, mock.Anything).
		Return(ExecutePromptOutput{Status: store.ResultStatusSucceeded}, nil).Times(n)
	env.OnActivity(a.FinalizeRun, mock.Anything, mock.MatchedBy(func(in FinalizeRunInput) bool {
		return in.RunID == spec.RunID
	})).Return(store.Run{Status: store.RunStatusCompleted}, nil).Once()
	env.OnWorkflow(AnalyzeRun, mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(RunWorkflow, runInput(t))

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

// TestRunWorkflowContinuesPastPromptError proves a single ExecutePrompt error
// does not abort the run: FinalizeRun still runs and the workflow completes.
func TestRunWorkflowContinuesPastPromptError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities
	const n = 2
	spec := specWithPrompts(t, n)

	env.OnActivity(a.LoadRunSpec, mock.Anything, mock.Anything).Return(spec, nil).Once()
	// One prompt errors, the other succeeds; ordering across the fan-out is not
	// guaranteed, so allow either outcome for each call.
	env.OnActivity(a.ExecutePrompt, mock.Anything, mock.Anything).
		Return(ExecutePromptOutput{}, errors.New("unexpected activity error")).Once()
	env.OnActivity(a.ExecutePrompt, mock.Anything, mock.Anything).
		Return(ExecutePromptOutput{Status: store.ResultStatusSucceeded}, nil).Once()
	env.OnActivity(a.FinalizeRun, mock.Anything, mock.MatchedBy(func(in FinalizeRunInput) bool {
		return in.RunID == spec.RunID
	})).Return(store.Run{Status: store.RunStatusPartial}, nil).Once()
	env.OnWorkflow(AnalyzeRun, mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(RunWorkflow, runInput(t))

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

// TestRunWorkflowZeroPrompts confirms the workflow wiring for an empty prompt
// snapshot: no ExecutePrompt runs and FinalizeRun is still called once.
// FinalizeRun is mocked here, so the resulting status classification is not
// exercised — that is covered against real SQL by
// TestActivitiesAgainstPostgres/FinalizeRun_with_zero_expected_results_is_failed.
func TestRunWorkflowZeroPrompts(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities
	spec := specWithPrompts(t, 0)

	env.OnActivity(a.LoadRunSpec, mock.Anything, mock.Anything).Return(spec, nil).Once()
	env.OnActivity(a.FinalizeRun, mock.Anything, mock.MatchedBy(func(in FinalizeRunInput) bool {
		return in.RunID == spec.RunID
	})).Return(store.Run{Status: store.RunStatusFailed}, nil).Once()
	env.OnWorkflow(AnalyzeRun, mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(RunWorkflow, runInput(t))

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

// TestRunWorkflowSurvivesAnalyzeRunFailure is ANA-7's core regression: a failed
// AnalyzeRun child workflow must not fail the parent run. FinalizeRun still runs
// (it precedes analysis) and the workflow completes without error — the run is
// left flagged for re-analysis (analysis_completed_at unset), not failed.
func TestRunWorkflowSurvivesAnalyzeRunFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities
	const n = 2
	spec := specWithPrompts(t, n)

	finalizeRan := false
	env.OnActivity(a.LoadRunSpec, mock.Anything, mock.Anything).Return(spec, nil).Once()
	env.OnActivity(a.ExecutePrompt, mock.Anything, mock.Anything).
		Return(ExecutePromptOutput{Status: store.ResultStatusSucceeded}, nil).Times(n)
	env.OnActivity(a.FinalizeRun, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { finalizeRan = true }).
		Return(store.Run{Status: store.RunStatusCompleted}, nil).Once()
	env.OnWorkflow(AnalyzeRun, mock.Anything, mock.Anything).
		Return(errors.New("analysis blew up")).Once()

	env.ExecuteWorkflow(RunWorkflow, runInput(t))

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error = %v, want nil (AnalyzeRun failure must not fail the run)", err)
	}
	if !finalizeRan {
		t.Fatal("FinalizeRun did not run")
	}
	env.AssertExpectations(t)
}
