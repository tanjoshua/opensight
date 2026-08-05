package workflows

import (
	"errors"
	"testing"

	"opensight/internal/llm"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

func genInput(t *testing.T) GenerateProfileWorkflowInput {
	t.Helper()
	return GenerateProfileWorkflowInput{
		AccountID:   mustID(t),
		BusinessID: mustID(t),
		Name:       "Acme Clinic",
		Website:    "https://acme.example",
	}
}

// assertFinalStage queries the completed workflow's stage handler and checks it
// reached want. It proves the handler registers under the right name and that
// the stage advances to its terminal value (never regressing on the tolerated
// failure paths). Mid-run assertions aren't attempted: this workflow has no
// timers, so the test env's mock clock offers no reliable intermediate point.
func assertFinalStage(t *testing.T, env *testsuite.TestWorkflowEnvironment, want string) {
	t.Helper()
	val, err := env.QueryWorkflow(GenerationStageQuery)
	if err != nil {
		t.Fatalf("query stage: %v", err)
	}
	var got string
	if err := val.Get(&got); err != nil {
		t.Fatalf("decode stage: %v", err)
	}
	if got != want {
		t.Fatalf("final stage = %q, want %q", got, want)
	}
}

func proposedOK() ProposeProfileOutput {
	return ProposeProfileOutput{
		Payload:  llm.ProposalPayload{LowConfidence: false},
		Proposed: true,
	}
}

// TestGenerateProfileWorkflowHappyPath: FetchSite succeeds, the combined
// ResearchAndPropose call validates, and the proposal is persisted verbatim
// (low_confidence unchanged).
func TestGenerateProfileWorkflowHappyPath(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.FetchSite, mock.Anything, mock.Anything).
		Return(FetchSiteOutput{Text: "site text"}, nil).Once()
	env.OnActivity(a.ProposeProfile, mock.Anything, mock.MatchedBy(func(in ProposeProfileInput) bool {
		return in.SiteText == "site text" && in.Location.Country == "SG"
	})).Return(proposedOK(), nil).Once()
	env.OnActivity(a.PersistProposal, mock.Anything, mock.MatchedBy(func(in PersistProposalInput) bool {
		return !in.Payload.LowConfidence
	})).Return(PersistProposalOutput{ProposalID: mustID(t)}, nil).Once()

	env.ExecuteWorkflow(GenerateProfileWorkflow, genInput(t))

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	assertFinalStage(t, env, GenerationStageDrafting)
	env.AssertExpectations(t)
}

// TestGenerateProfileWorkflowFetchFailsForcesLowConfidence: FetchSite failing
// proceeds with empty site text and, because the model did not read the site
// itself, stamps low_confidence on the persisted proposal even though the model
// returned low_confidence false.
func TestGenerateProfileWorkflowFetchFailsForcesLowConfidence(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.FetchSite, mock.Anything, mock.Anything).
		Return(FetchSiteOutput{}, errors.New("site unreachable"))
	env.OnActivity(a.ProposeProfile, mock.Anything, mock.MatchedBy(func(in ProposeProfileInput) bool {
		return in.SiteText == ""
	})).Return(ProposeProfileOutput{
		Payload:       llm.ProposalPayload{LowConfidence: false},
		Proposed:      true,
		OpenedOwnSite: false,
	}, nil).Once()
	persisted := false
	env.OnActivity(a.PersistProposal, mock.Anything, mock.MatchedBy(func(in PersistProposalInput) bool {
		persisted = in.Payload.LowConfidence
		return persisted
	})).Return(PersistProposalOutput{ProposalID: mustID(t)}, nil).Once()

	env.ExecuteWorkflow(GenerateProfileWorkflow, genInput(t))

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	if !persisted {
		t.Fatal("expected low_confidence true on persisted payload")
	}
	// The tolerated FetchSite-failure path still advances the stage to the end.
	assertFinalStage(t, env, GenerationStageDrafting)
	env.AssertExpectations(t)
}

// TestGenerateProfileWorkflowFetchFailsButModelReadSite: FetchSite failing does
// NOT force low_confidence when the model opened the site itself
// (OpenedOwnSite), so the model's own low_confidence=false judgement stands.
func TestGenerateProfileWorkflowFetchFailsButModelReadSite(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.FetchSite, mock.Anything, mock.Anything).
		Return(FetchSiteOutput{}, errors.New("site unreachable"))
	env.OnActivity(a.ProposeProfile, mock.Anything, mock.Anything).
		Return(ProposeProfileOutput{
			Payload:       llm.ProposalPayload{LowConfidence: false},
			Proposed:      true,
			OpenedOwnSite: true,
		}, nil).Once()
	env.OnActivity(a.PersistProposal, mock.Anything, mock.MatchedBy(func(in PersistProposalInput) bool {
		return !in.Payload.LowConfidence
	})).Return(PersistProposalOutput{ProposalID: mustID(t)}, nil).Once()

	env.ExecuteWorkflow(GenerateProfileWorkflow, genInput(t))

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

// TestGenerateProfileWorkflowProposeFailsWorkflowFails: the combined call
// erroring after Temporal retries fails the workflow — no proposal is persisted.
func TestGenerateProfileWorkflowProposeFailsWorkflowFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.FetchSite, mock.Anything, mock.Anything).
		Return(FetchSiteOutput{Text: "site text"}, nil).Once()
	env.OnActivity(a.ProposeProfile, mock.Anything, mock.Anything).
		Return(ProposeProfileOutput{}, errors.New("openai unavailable"))

	env.ExecuteWorkflow(GenerateProfileWorkflow, genInput(t))

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow error when the propose call fails")
	}
	env.AssertExpectations(t)
}

// TestGenerateProfileWorkflowProposalInvalid: a proposal that never validated
// (Proposed=false) is a hard failure — no proposal row is written.
func TestGenerateProfileWorkflowProposalInvalid(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.FetchSite, mock.Anything, mock.Anything).
		Return(FetchSiteOutput{Text: "site text"}, nil).Once()
	env.OnActivity(a.ProposeProfile, mock.Anything, mock.Anything).
		Return(ProposeProfileOutput{Proposed: false}, nil).Once()

	env.ExecuteWorkflow(GenerateProfileWorkflow, genInput(t))

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow error when proposal failed validation")
	}
	env.AssertExpectations(t)
}
