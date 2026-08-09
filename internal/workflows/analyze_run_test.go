package workflows

import (
	"errors"
	"testing"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

// TestAnalyzeRunFansOutAndReconciles proves the fan-out count matches the spec's
// result ids and only Analyzed==true outputs reach ReconcileEntities.
func TestAnalyzeRunFansOutAndReconciles(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	accountID := mustID(t)
	runID := mustID(t)
	businessID := mustID(t)
	r1, r2, r3 := mustID(t), mustID(t), mustID(t)
	spec := store.AnalyzeRunSpec{BusinessID: businessID, ResultIDs: []domain.ID{r1, r2, r3}}

	env.OnActivity(a.LoadAnalyzeRunSpec, mock.Anything, mock.Anything).Return(spec, nil).Once()

	// r1 analyzed with an entity, r2 not analyzed (must be excluded), r3 analyzed.
	env.OnActivity(a.AnalyzeResult, mock.Anything, mock.MatchedBy(func(in AnalyzeResultInput) bool {
		return in.ResultID == r1
	})).Return(AnalyzeResultOutput{ResultID: r1, Analyzed: true,
		Entities: []llm.EntityWithCitations{{
			Entity:     llm.ExtractedEntity{VerbatimName: "Rival Clinic", Excerpt: "Rival Clinic is good."},
			CiteOrders: []int{},
		}}}, nil).Once()
	env.OnActivity(a.AnalyzeResult, mock.Anything, mock.MatchedBy(func(in AnalyzeResultInput) bool {
		return in.ResultID == r2
	})).Return(AnalyzeResultOutput{ResultID: r2, Analyzed: false}, nil).Once()
	env.OnActivity(a.AnalyzeResult, mock.Anything, mock.MatchedBy(func(in AnalyzeResultInput) bool {
		return in.ResultID == r3
	})).Return(AnalyzeResultOutput{ResultID: r3, Analyzed: true}, nil).Once()

	var gotResults []ResultEntities
	env.OnActivity(a.ReconcileEntities, mock.Anything, mock.MatchedBy(func(in ReconcileEntitiesInput) bool {
		gotResults = in.Results
		return in.RunID == runID && in.BusinessID == businessID
	})).Return(ReconcileEntitiesOutput{}, nil).Once()

	env.ExecuteWorkflow(AnalyzeRun, AnalyzeRunInput{AccountID: accountID, RunID: runID})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	// r2 (Analyzed=false) excluded: reconcile sees r1 and r3 only.
	if len(gotResults) != 2 {
		t.Fatalf("reconcile results = %d, want 2 (the unanalyzed result excluded)", len(gotResults))
	}
	env.AssertExpectations(t)
}

// TestAnalyzeRunSurvivesResultFailure proves a single AnalyzeResult failure does
// not stop ReconcileEntities from running over the results that did succeed.
func TestAnalyzeRunSurvivesResultFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	runID := mustID(t)
	businessID := mustID(t)
	r1, r2 := mustID(t), mustID(t)
	spec := store.AnalyzeRunSpec{BusinessID: businessID, ResultIDs: []domain.ID{r1, r2}}

	env.OnActivity(a.LoadAnalyzeRunSpec, mock.Anything, mock.Anything).Return(spec, nil).Once()
	env.OnActivity(a.AnalyzeResult, mock.Anything, mock.MatchedBy(func(in AnalyzeResultInput) bool {
		return in.ResultID == r1
	})).Return(AnalyzeResultOutput{}, errors.New("extraction blew up")).Once()
	env.OnActivity(a.AnalyzeResult, mock.Anything, mock.MatchedBy(func(in AnalyzeResultInput) bool {
		return in.ResultID == r2
	})).Return(AnalyzeResultOutput{ResultID: r2, Analyzed: true}, nil).Once()

	reconcileRan := false
	env.OnActivity(a.ReconcileEntities, mock.Anything, mock.MatchedBy(func(in ReconcileEntitiesInput) bool {
		reconcileRan = true
		return len(in.Results) == 1 // only r2 survived
	})).Return(ReconcileEntitiesOutput{}, nil).Once()

	env.ExecuteWorkflow(AnalyzeRun, AnalyzeRunInput{AccountID: mustID(t), RunID: runID})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	if !reconcileRan {
		t.Fatal("ReconcileEntities did not run despite one result failing")
	}
	env.AssertExpectations(t)
}
