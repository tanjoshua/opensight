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
		TenantID:    mustID(t),
		BusinessID:  mustID(t),
		Name:        "Acme Clinic",
		Website:     "https://acme.example",
		PromptLimit: 20,
	}
}

func proposedOK() ProposeProfileOutput {
	return ProposeProfileOutput{
		Payload:  llm.ProposalPayload{LowConfidence: false},
		Proposed: true,
	}
}

// TestGenerateProfileWorkflowHappyPath: both evidence sources succeed, the
// proposal validates, and it is persisted verbatim (low_confidence unchanged).
func TestGenerateProfileWorkflowHappyPath(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.FetchSite, mock.Anything, mock.Anything).
		Return(FetchSiteOutput{Text: "site text"}, nil).Once()
	env.OnActivity(a.ResearchBusiness, mock.Anything, mock.Anything).
		Return(ResearchBusinessOutput{Summary: "research"}, nil).Once()
	env.OnActivity(a.ProposeProfile, mock.Anything, mock.Anything).
		Return(proposedOK(), nil).Once()
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
	env.AssertExpectations(t)
}

// TestGenerateProfileWorkflowFetchFailsForcesLowConfidence: FetchSite failing
// proceeds research-only and stamps low_confidence on the persisted proposal,
// even though ProposeProfile returned low_confidence false.
func TestGenerateProfileWorkflowFetchFailsForcesLowConfidence(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.FetchSite, mock.Anything, mock.Anything).
		Return(FetchSiteOutput{}, errors.New("site unreachable"))
	env.OnActivity(a.ResearchBusiness, mock.Anything, mock.Anything).
		Return(ResearchBusinessOutput{Summary: "research"}, nil).Once()
	env.OnActivity(a.ProposeProfile, mock.Anything, mock.MatchedBy(func(in ProposeProfileInput) bool {
		return in.SiteText == ""
	})).Return(proposedOK(), nil).Once()
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
	env.AssertExpectations(t)
}

// TestGenerateProfileWorkflowResearchFailsStillSucceeds: research failing is
// survivable as long as FetchSite gave usable evidence; low_confidence is not
// forced (the fetch succeeded).
func TestGenerateProfileWorkflowResearchFailsStillSucceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.FetchSite, mock.Anything, mock.Anything).
		Return(FetchSiteOutput{Text: "site text"}, nil).Once()
	env.OnActivity(a.ResearchBusiness, mock.Anything, mock.Anything).
		Return(ResearchBusinessOutput{}, errors.New("web search down"))
	env.OnActivity(a.ProposeProfile, mock.Anything, mock.MatchedBy(func(in ProposeProfileInput) bool {
		return in.ResearchSummary == "" && in.SiteText == "site text"
	})).Return(proposedOK(), nil).Once()
	env.OnActivity(a.PersistProposal, mock.Anything, mock.MatchedBy(func(in PersistProposalInput) bool {
		return !in.Payload.LowConfidence
	})).Return(PersistProposalOutput{ProposalID: mustID(t)}, nil).Once()

	env.ExecuteWorkflow(GenerateProfileWorkflow, genInput(t))

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

// TestGenerateProfileWorkflowBothSourcesFail: with neither evidence source, the
// workflow fails before ever calling ProposeProfile/PersistProposal.
func TestGenerateProfileWorkflowBothSourcesFail(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.FetchSite, mock.Anything, mock.Anything).
		Return(FetchSiteOutput{}, errors.New("site unreachable"))
	env.OnActivity(a.ResearchBusiness, mock.Anything, mock.Anything).
		Return(ResearchBusinessOutput{}, errors.New("web search down"))

	env.ExecuteWorkflow(GenerateProfileWorkflow, genInput(t))

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow error when both sources fail")
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
	env.OnActivity(a.ResearchBusiness, mock.Anything, mock.Anything).
		Return(ResearchBusinessOutput{Summary: "research"}, nil).Once()
	env.OnActivity(a.ProposeProfile, mock.Anything, mock.Anything).
		Return(ProposeProfileOutput{Proposed: false}, nil).Once()

	env.ExecuteWorkflow(GenerateProfileWorkflow, genInput(t))

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow error when proposal failed validation")
	}
	env.AssertExpectations(t)
}
