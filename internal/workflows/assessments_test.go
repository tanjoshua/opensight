package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	plan := AssessmentPlan{GenerationID: mustID(t), Status: "RUNNING", Entries: []store.ModulePlanEntry{
		{AssessorKey: "influential-source", ModuleVersion: 1, RequiredCollectors: []string{visibility.CollectorMonitoring}},
		{AssessorKey: "tracked-topic", ModuleVersion: 1, RequiredCollectors: []string{visibility.CollectorMonitoring, visibility.CollectorOwnedSite}},
	}}

	env.OnActivity(activities.ResolveAssessmentPlan, mock.Anything, in).Return(plan, nil).Once()
	env.OnActivity(activities.CollectAssessmentEvidence, mock.Anything, mock.MatchedBy(func(input CollectEvidenceInput) bool { return input.CollectorKey == visibility.CollectorMonitoring })).Return(visibility.EvidenceArtifact{CollectorKey: visibility.CollectorMonitoring, CollectorVersion: 1, PayloadVersion: 1, CheckedAt: time.Now(), Payload: []byte(`{}`)}, nil).Once()
	env.OnActivity(activities.CollectAssessmentEvidence, mock.Anything, mock.MatchedBy(func(input CollectEvidenceInput) bool { return input.CollectorKey == visibility.CollectorOwnedSite })).Return(visibility.EvidenceArtifact{}, temporal.NewNonRetryableApplicationError("site unavailable", "InspectionFailed", errors.New("timeout"))).Once()
	env.OnActivity(activities.RunPracticeAssessor, mock.Anything, mock.MatchedBy(func(input RunAssessorInput) bool {
		return input.Entry.AssessorKey == "influential-source" && len(input.Artifacts) == 1
	})).Return(RunAssessorOutput{}, nil).Once()
	env.OnActivity(activities.PublishAssessments, mock.Anything, mock.MatchedBy(func(input PublishAssessmentsInput) bool {
		return len(input.Outputs) == 1 && len(input.Outcomes) == 2 && input.Outcomes[0].Status == "SUCCEEDED" && input.Outcomes[1].Status == "SKIPPED"
	})).Return(nil).Once()

	env.ExecuteWorkflow(AssessmentWorkflow, in)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestRobotsDeniesOAIUsesSpecificGroupAndAllowTie(t *testing.T) {
	contentPaths := []string{"/", "/blog/opening-hours"}
	tests := []struct {
		name, body string
		paths      []string
		denied     bool
	}{
		{name: "wildcard denial", body: "User-agent: *\nDisallow: /", paths: contentPaths, denied: true},
		{name: "specific override", body: "User-agent: *\nDisallow: /\n\nUser-agent: OAI-SearchBot\nAllow: /", paths: contentPaths, denied: false},
		{name: "allow wins equal length", body: "User-agent: OAI-SearchBot\nDisallow: /\nAllow: /", paths: contentPaths, denied: false},
		{name: "unrelated bot", body: "User-agent: OtherBot\nDisallow: /", paths: contentPaths, denied: false},
		{name: "content path denied", body: "User-agent: *\nDisallow: /blog", paths: contentPaths, denied: true},
		{name: "content path outside denied prefix", body: "User-agent: *\nDisallow: /blog", paths: []string{"/", "/services"}, denied: false},
		{name: "content path allowed under denied prefix", body: "User-agent: *\nDisallow: /blog\nAllow: /blog/opening-hours", paths: contentPaths, denied: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := robotsDeniesOAI(tt.body, tt.paths); got != tt.denied {
				t.Fatalf("robotsDeniesOAI = %v, want %v", got, tt.denied)
			}
		})
	}
}

// TestScanOwnedSiteVerdicts covers the signals the only shipping practice
// depends on, and asserts the scan reads each URL exactly once.
func TestScanOwnedSiteVerdicts(t *testing.T) {
	const indexable = `<html><body><main>Example Clinic page text</main></body></html>`
	const noIndexed = `<html><head><meta name="robots" content="noindex,follow"></head><body><main>Example Clinic page text</main></body></html>`
	tests := []struct {
		name         string
		robotsStatus int
		robotsBody   string
		// homeStatus, when set, overrides pageStatus/pageBody for the homepage.
		homeStatus int
		homeBody   string
		pageStatus int
		pageBody   string
		want       visibility.AssessmentStatus
	}{
		{name: "no robots file", robotsStatus: http.StatusNotFound, pageStatus: http.StatusOK, pageBody: indexable, want: visibility.StatusMet},
		{name: "robots unreachable", robotsStatus: http.StatusInternalServerError, pageStatus: http.StatusOK, pageBody: indexable, want: visibility.StatusUnknown},
		{name: "content path denied", robotsStatus: http.StatusOK, robotsBody: "User-agent: *\nDisallow: /services", pageStatus: http.StatusOK, pageBody: indexable, want: visibility.StatusNotMet},
		{name: "homepage noindex with indexable subpages", robotsStatus: http.StatusNotFound, homeStatus: http.StatusOK, homeBody: noIndexed, pageStatus: http.StatusOK, pageBody: indexable, want: visibility.StatusNotMet},
		{name: "noindex away from the homepage", robotsStatus: http.StatusNotFound, homeStatus: http.StatusOK, homeBody: indexable, pageStatus: http.StatusOK, pageBody: noIndexed, want: visibility.StatusMet},
		{name: "every retrieved page noindex", robotsStatus: http.StatusNotFound, homeStatus: http.StatusNotFound, pageStatus: http.StatusOK, pageBody: noIndexed, want: visibility.StatusNotMet},
		{name: "homepage auth barrier with public subpages", robotsStatus: http.StatusNotFound, homeStatus: http.StatusUnauthorized, pageStatus: http.StatusOK, pageBody: indexable, want: visibility.StatusNotMet},
		{name: "site-wide auth barrier", robotsStatus: http.StatusNotFound, pageStatus: http.StatusUnauthorized, want: visibility.StatusNotMet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := map[string]int{}
			fetcher := &siteFetcher{client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests[req.URL.Path]++
				if req.URL.Path == "/robots.txt" {
					return testFetchResponse(req, tt.robotsStatus, "text/plain", tt.robotsBody), nil
				}
				status, body := tt.pageStatus, tt.pageBody
				if req.URL.Path == "/" && tt.homeStatus != 0 {
					status, body = tt.homeStatus, tt.homeBody
				}
				return testFetchResponse(req, status, "text/html", body), nil
			}))}

			scan := scanOwnedSite(context.Background(), fetcher, "http://example.com")
			if got := assessSearchAccessStatus(t, scan); got != tt.want {
				t.Fatalf("assessment status = %s, want %s (scan %+v)", got, tt.want, scan)
			}
			for path, count := range requests {
				if count != 1 {
					t.Fatalf("requested %s %d times, want exactly one request per URL", path, count)
				}
			}
		})
	}
}

func assessSearchAccessStatus(t *testing.T, scan visibility.OwnedSiteScan) visibility.AssessmentStatus {
	t.Helper()
	payload, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}
	view := visibility.NewEvidenceView([]visibility.EvidenceArtifact{{CollectorKey: visibility.CollectorOwnedSite, PayloadVersion: 1, Payload: payload}})
	for _, assessor := range visibility.Assessors() {
		if assessor.Manifest().Key != "search-access" {
			continue
		}
		drafts, err := assessor.Assess(context.Background(), view, nil)
		if err != nil || len(drafts) != 1 {
			t.Fatalf("assess search access: %v (drafts %d)", err, len(drafts))
		}
		return drafts[0].Status
	}
	t.Fatal("search-access assessor is not registered")
	return ""
}
