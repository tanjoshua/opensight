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
// ExecutePrompt calls and one FinalizeRun with ExpectedResults == N.
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
		return in.ExpectedResults == n && in.RunID == spec.RunID
	})).Return(store.Run{Status: store.RunStatusCompleted}, nil).Once()

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
		return in.ExpectedResults == n
	})).Return(store.Run{Status: store.RunStatusPartial}, nil).Once()

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
// snapshot: no ExecutePrompt runs and FinalizeRun is still called once with
// ExpectedResults == 0. FinalizeRun is mocked here, so the resulting status
// classification is not exercised — that is covered against real SQL by
// TestActivitiesAgainstPostgres/FinalizeRun_with_zero_expected_results_is_failed.
func TestRunWorkflowZeroPrompts(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities
	spec := specWithPrompts(t, 0)

	env.OnActivity(a.LoadRunSpec, mock.Anything, mock.Anything).Return(spec, nil).Once()
	env.OnActivity(a.FinalizeRun, mock.Anything, mock.MatchedBy(func(in FinalizeRunInput) bool {
		return in.ExpectedResults == 0
	})).Return(store.Run{Status: store.RunStatusFailed}, nil).Once()

	env.ExecuteWorkflow(RunWorkflow, runInput(t))

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}
