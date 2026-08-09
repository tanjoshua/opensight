package workflows

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"opensight/internal/visibility"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
)

func TestAssessmentWorkflowFinderFailureDoesNotPublish(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	activities := &Activities{}
	env.RegisterActivity(activities.RunSiteAudit)
	env.RegisterActivity(activities.RunFinders)
	env.RegisterActivity(activities.PublishImproveRun)

	env.OnActivity(activities.RunSiteAudit, mock.Anything, mock.Anything).Return(SiteAuditResult{}, nil).Once()
	// Twice, not three times: an attempt re-pays for every research fetch and the
	// classification call, so RunFinders is granted one retry rather than two.
	env.OnActivity(activities.RunFinders, mock.Anything, mock.Anything).Return(nil, errors.New("source classification failed validation")).Times(2)

	env.ExecuteWorkflow(AssessmentWorkflow, AssessmentWorkflowInput{})
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow succeeded despite source classification failure")
	}
	// No PublishImproveRun expectation is registered: any publication would fail
	// this test. The previously current audit/findings therefore stay untouched.
	env.AssertExpectations(t)
}

// TestAssessmentWorkflowPublishesWhatTheFindersProduced pins the shape of the
// run: audit, then finders over that audit, then one publication carrying both.
// A failure anywhere publishes nothing, which is what keeps last week's advice
// intact rather than half-replaced.
func TestAssessmentWorkflowPublishesWhatTheFindersProduced(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	activities := &Activities{}
	env.RegisterActivity(activities.RunSiteAudit)
	env.RegisterActivity(activities.RunFinders)
	env.RegisterActivity(activities.PublishImproveRun)

	audit := SiteAuditResult{
		Checks:      []visibility.CheckResult{{Key: visibility.CheckRobotsOAISearchBot, Outcome: visibility.CheckFail, Detail: "blocked"}},
		PagesRead:   4,
		SiteContent: "bounded customer-site content",
	}
	findings := []visibility.Finding{{Key: "site-audit:robots_allows_oai_searchbot", Source: visibility.SourceSiteAudit, Blocking: true}}

	env.OnActivity(activities.RunSiteAudit, mock.Anything, mock.Anything).Return(audit, nil).Once()
	env.OnActivity(activities.RunFinders, mock.Anything, mock.MatchedBy(func(in FindImprovementsInput) bool {
		return len(in.Audit.Checks) == 1 && in.Audit.PagesRead == 4 && in.Audit.SiteContent == "bounded customer-site content"
	})).Return(findings, nil).Once()
	env.OnActivity(activities.PublishImproveRun, mock.Anything, mock.MatchedBy(func(in PublishImproveRunInput) bool {
		return len(in.Findings) == 1 && in.Findings[0].Blocking && in.Audit.PagesRead == 4
	})).Return(nil).Once()

	env.ExecuteWorkflow(AssessmentWorkflow, AssessmentWorkflowInput{})
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

// TestAssessmentActivitiesAreBudgetedForTheirOwnWork guards the one thing a
// shared set of activity options gets wrong: the heaviest activity inherits the
// smallest budget. The bounds are computed from the same constants the crawls
// spend, so raising a request budget without raising the matching timeout fails
// here rather than in production, where it surfaces as a timed-out run that
// silently republishes nothing.
func TestAssessmentActivitiesAreBudgetedForTheirOwnWork(t *testing.T) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	activities := &Activities{}
	env.RegisterActivity(activities.RunSiteAudit)
	env.RegisterActivity(activities.RunFinders)
	env.RegisterActivity(activities.PublishImproveRun)

	env.OnActivity(activities.RunSiteAudit, mock.Anything, mock.Anything).Return(SiteAuditResult{}, nil).Once()
	env.OnActivity(activities.RunFinders, mock.Anything, mock.Anything).Return([]visibility.Finding{}, nil).Once()
	env.OnActivity(activities.PublishImproveRun, mock.Anything, mock.Anything).Return(nil).Once()

	budgets := map[string]time.Duration{}
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		budgets[info.ActivityType.Name] = info.StartToCloseTimeout
	})

	env.ExecuteWorkflow(AssessmentWorkflow, AssessmentWorkflowInput{})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	// Both crawls fetch serially with each request capped at fetchSiteTimeout, so
	// these products are what one attempt can actually take.
	if want := fetchSiteMaxRequests * fetchSiteTimeout; budgets["RunSiteAudit"] < want {
		t.Errorf("RunSiteAudit budget %s is below its %s crawl worst case", budgets["RunSiteAudit"], want)
	}
	if want := visibility.ResearchURLBudget * fetchSiteTimeout; budgets["RunFinders"] < want {
		t.Errorf("RunFinders budget %s is below its %s research worst case", budgets["RunFinders"], want)
	}
	// A publication is one transaction. Sizing it like the crawls would be the
	// same mistake in the other direction: a wedged write held for minutes.
	if budgets["PublishImproveRun"] >= budgets["RunFinders"] {
		t.Errorf("PublishImproveRun budget %s is not scaled to a single transaction", budgets["PublishImproveRun"])
	}
}

func TestRobotsDeniesUsesSpecificGroupAndAllowTie(t *testing.T) {
	contentPaths := []string{"/", "/blog/opening-hours"}
	// A conventional way to admit one crawler and exclude the rest: the empty
	// Disallow allows the named group everything, so it must not fall through
	// to the wildcard group's blanket denial.
	emptyDisallow := "User-agent: OAI-SearchBot\nDisallow:\n\nUser-agent: *\nDisallow: /"
	tests := []struct {
		name, body string
		agent      string
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
		// The per-agent verdicts are what let the checklist report the search
		// crawler, the live fetcher and the training crawler separately.
		{name: "training crawler denied alone", body: "User-agent: GPTBot\nDisallow: /", agent: visibility.AgentGPTBot, paths: contentPaths, denied: true},
		{name: "search crawler unaffected by a GPTBot rule", body: "User-agent: GPTBot\nDisallow: /", agent: visibility.AgentOAISearchBot, paths: contentPaths, denied: false},
		{name: "live fetcher denied by its own group", body: "User-agent: *\nAllow: /\n\nUser-agent: ChatGPT-User\nDisallow: /", agent: visibility.AgentChatGPTUser, paths: contentPaths, denied: true},
		{name: "empty disallow allows the named group", body: emptyDisallow, agent: visibility.AgentOAISearchBot, paths: contentPaths, denied: false},
		{name: "empty disallow leaves other crawlers denied", body: emptyDisallow, agent: visibility.AgentGPTBot, paths: contentPaths, denied: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := tt.agent
			if agent == "" {
				agent = visibility.AgentOAISearchBot
			}
			if got := robotsDenies(tt.body, agent, tt.paths); got != tt.denied {
				t.Fatalf("robotsDenies(%s) = %v, want %v", agent, got, tt.denied)
			}
		})
	}
}

func TestRobotsSitemapsReadsGlobalDirective(t *testing.T) {
	body := "Sitemap: https://example.com/sitemap.xml\nUser-agent: *\nDisallow: /admin\n# Sitemap: https://example.com/commented.xml\nsitemap:  https://example.com/news.xml"
	got := robotsSitemaps(body)
	want := []string{"https://example.com/sitemap.xml", "https://example.com/news.xml"}
	if len(got) != len(want) {
		t.Fatalf("robotsSitemaps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("robotsSitemaps = %v, want %v", got, want)
		}
	}
}

// TestScanOwnedSiteVerdicts covers the access signals the scan exists to
// establish, and asserts it reads each URL exactly once. Only the access group
// is asserted: it is the part decided by status codes, headers and robots.txt,
// which is what this test drives.
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
		// want names the access checks not expected to pass; the rest must pass.
		want map[string]visibility.CheckOutcome
	}{
		{name: "no robots file", robotsStatus: http.StatusNotFound, pageStatus: http.StatusOK, pageBody: indexable, want: nil},
		{name: "robots unreachable", robotsStatus: http.StatusInternalServerError, pageStatus: http.StatusOK, pageBody: indexable, want: map[string]visibility.CheckOutcome{visibility.CheckRobotsOAISearchBot: visibility.CheckCouldNotVerify, visibility.CheckRobotsChatGPTUser: visibility.CheckCouldNotVerify, visibility.CheckRobotsGPTBot: visibility.CheckCouldNotVerify}},
		{name: "content path denied", robotsStatus: http.StatusOK, robotsBody: "User-agent: *\nDisallow: /services", pageStatus: http.StatusOK, pageBody: indexable, want: map[string]visibility.CheckOutcome{visibility.CheckRobotsOAISearchBot: visibility.CheckFail, visibility.CheckRobotsChatGPTUser: visibility.CheckFail, visibility.CheckRobotsGPTBot: visibility.CheckNotApplicable}},
		{name: "homepage noindex with indexable subpages", robotsStatus: http.StatusNotFound, homeStatus: http.StatusOK, homeBody: noIndexed, pageStatus: http.StatusOK, pageBody: indexable, want: map[string]visibility.CheckOutcome{visibility.CheckPagesAllowIndexing: visibility.CheckFail}},
		{name: "noindex away from the homepage", robotsStatus: http.StatusNotFound, homeStatus: http.StatusOK, homeBody: indexable, pageStatus: http.StatusOK, pageBody: noIndexed, want: nil},
		{name: "every retrieved page noindex", robotsStatus: http.StatusNotFound, homeStatus: http.StatusNotFound, pageStatus: http.StatusOK, pageBody: noIndexed, want: map[string]visibility.CheckOutcome{visibility.CheckPagesAllowIndexing: visibility.CheckFail}},
		{name: "homepage auth barrier with public subpages", robotsStatus: http.StatusNotFound, homeStatus: http.StatusUnauthorized, pageStatus: http.StatusOK, pageBody: indexable, want: map[string]visibility.CheckOutcome{visibility.CheckNoLoginWall: visibility.CheckFail}},
		{name: "site-wide auth barrier", robotsStatus: http.StatusNotFound, pageStatus: http.StatusUnauthorized, want: map[string]visibility.CheckOutcome{visibility.CheckNoLoginWall: visibility.CheckFail, visibility.CheckSiteReachable: visibility.CheckFail}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := map[string]int{}
			fetcher := &siteFetcher{client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests[req.URL.Host+req.URL.Path]++
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
			for key, got := range accessOutcomes(scan) {
				want, named := tt.want[key]
				if !named {
					want = visibility.CheckPass
				}
				if got != want {
					t.Fatalf("check %s = %s, want %s (scan %+v)", key, got, want, scan)
				}
			}
			for path, count := range requests {
				if count != 1 {
					t.Fatalf("requested %s %d times, want exactly one request per URL", path, count)
				}
			}
		})
	}
}

// TestScanOwnedSiteCapturesPageFactsWithoutExtraRequests is the guard on the
// premise of the structure and identity practices: every fact they read comes
// out of markup the scan already fetched for search access, so adding those
// checks cost no additional request.
func TestScanOwnedSiteCapturesPageFactsWithoutExtraRequests(t *testing.T) {
	const home = `<html><head>` +
		`<title>Example Clinic — Endodontics</title>` +
		`<meta name="description" content="Root canal specialists.">` +
		`<link rel="canonical" href="https://example.com/">` +
		`<script type="application/ld+json">{"@type":"Dentist","telephone":"+65 6123 4567"}</script>` +
		`</head><body><h1>Example Clinic</h1><main>Example Clinic page text</main>` +
		`<a href="tel:+6561234567">Call us</a></body></html>`
	const other = `<html><head><title>About</title></head><body><main>About Example Clinic</main></body></html>`

	requests := map[string]int{}
	fetcher := &siteFetcher{client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests[req.URL.Path]++
		switch req.URL.Path {
		case "/robots.txt":
			return testFetchResponse(req, http.StatusOK, "text/plain", "Sitemap: https://example.com/sitemap.xml\nUser-agent: *\nAllow: /"), nil
		case "/":
			return testFetchResponse(req, http.StatusOK, "text/html", home), nil
		default:
			return testFetchResponse(req, http.StatusOK, "text/html", other), nil
		}
	}))}

	scan := scanOwnedSite(context.Background(), fetcher, "http://example.com")
	for path, count := range requests {
		if count != 1 {
			t.Fatalf("requested %s %d times, want exactly one request per URL", path, count)
		}
	}
	if len(scan.Pages) == 0 {
		t.Fatal("scan captured no page facts")
	}
	first := scan.Pages[0]
	if first.Title != "Example Clinic — Endodontics" {
		t.Errorf("title = %q", first.Title)
	}
	if first.H1 != "Example Clinic" {
		t.Errorf("h1 = %q", first.H1)
	}
	if first.MetaDescription != "Root canal specialists." {
		t.Errorf("meta description = %q", first.MetaDescription)
	}
	if first.Canonical != "https://example.com/" {
		t.Errorf("canonical = %q", first.Canonical)
	}
	if !first.HasTelLink {
		t.Error("tel: link was not captured")
	}
	// JSON-LD lives inside a script element, which the text walk skips; capturing
	// it needs the parse to take it before that skip applies.
	if len(first.JSONLD) != 1 || !strings.Contains(first.JSONLD[0], "+65 6123 4567") {
		t.Errorf("json-ld = %v", first.JSONLD)
	}
	if len(scan.RobotsSitemaps) != 1 {
		t.Errorf("robots sitemaps = %v", scan.RobotsSitemaps)
	}
	if scan.AgentDenied[visibility.AgentOAISearchBot] {
		t.Error("allow-all robots reported a denial")
	}
}

// accessOutcomes runs the audit and keeps the access group, which is the part
// the scan itself decides.
func accessOutcomes(scan visibility.OwnedSiteScan) map[string]visibility.CheckOutcome {
	groups := map[string]string{}
	for _, check := range visibility.Catalog() {
		groups[check.Key] = check.Group
	}
	out := map[string]visibility.CheckOutcome{}
	for _, result := range visibility.Audit(scan) {
		if groups[result.Key] == visibility.GroupAccess {
			out[result.Key] = result.Outcome
		}
	}
	return out
}
