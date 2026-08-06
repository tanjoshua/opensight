package workflows

import (
	"errors"
	"testing"
	"time"

	"opensight/internal/store"
	"opensight/internal/visibility"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestAssessmentWorkflowSharesCollectorsAndPreservesPartialProgress(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var activities *Activities
	in := AssessmentWorkflowInput{AccountID: mustID(t), BusinessID: mustID(t), RunID: mustID(t)}
	plan := AssessmentPlan{GenerationID: mustID(t), Entries: []store.ModulePlanEntry{
		{AssessorKey: "influential-source", ModuleVersion: 1, Mode: visibility.RolloutShadow, RequiredCollectors: []string{visibility.CollectorMonitoring}},
		{AssessorKey: "tracked-topic", ModuleVersion: 1, Mode: visibility.RolloutShadow, RequiredCollectors: []string{visibility.CollectorMonitoring, visibility.CollectorOwnedSite}},
	}}

	env.OnActivity(activities.ResolveAssessmentPlan, mock.Anything, in).Return(plan, nil).Once()
	env.OnActivity(activities.CollectAssessmentEvidence, mock.Anything, mock.MatchedBy(func(input CollectEvidenceInput) bool { return input.CollectorKey == visibility.CollectorMonitoring })).Return(visibility.EvidenceArtifact{CollectorKey: visibility.CollectorMonitoring, CollectorVersion: 1, PayloadVersion: 1, CheckedAt: time.Now(), Payload: []byte(`{}`)}, nil).Once()
	env.OnActivity(activities.CollectAssessmentEvidence, mock.Anything, mock.MatchedBy(func(input CollectEvidenceInput) bool { return input.CollectorKey == visibility.CollectorOwnedSite })).Return(visibility.EvidenceArtifact{}, temporal.NewNonRetryableApplicationError("site unavailable", "InspectionFailed", errors.New("timeout"))).Once()
	env.OnActivity(activities.RunPracticeAssessor, mock.Anything, mock.MatchedBy(func(input RunAssessorInput) bool {
		return input.Entry.AssessorKey == "influential-source" && len(input.Artifacts) == 1
	})).Return(RunAssessorOutput{}, nil).Once()
	env.OnActivity(activities.CompileOpportunities, mock.Anything, mock.MatchedBy(func(input CompileOpportunitiesInput) bool { return input.HadFailure && len(input.Outputs) == 1 })).Return(nil).Once()

	env.ExecuteWorkflow(AssessmentWorkflow, in)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestRobotsDeniesOAIUsesSpecificGroupAndAllowTie(t *testing.T) {
	tests := []struct {
		name, body string
		denied     bool
	}{
		{name: "wildcard denial", body: "User-agent: *\nDisallow: /", denied: true},
		{name: "specific override", body: "User-agent: *\nDisallow: /\n\nUser-agent: OAI-SearchBot\nAllow: /", denied: false},
		{name: "allow wins equal length", body: "User-agent: OAI-SearchBot\nDisallow: /\nAllow: /", denied: false},
		{name: "unrelated bot", body: "User-agent: OtherBot\nDisallow: /", denied: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := robotsDeniesOAI(tt.body); got != tt.denied {
				t.Fatalf("robotsDeniesOAI = %v, want %v", got, tt.denied)
			}
		})
	}
}

func TestHTMLNoIndex(t *testing.T) {
	if !htmlNoIndex([]byte(`<html><head><meta content='noindex,follow' name='robots'></head></html>`)) {
		t.Fatal("expected noindex meta to be detected")
	}
	if htmlNoIndex([]byte(`<html><head><meta content='index,follow' name='robots'></head></html>`)) {
		t.Fatal("did not expect index meta to be blocked")
	}
}
