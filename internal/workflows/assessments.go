package workflows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"
	"opensight/internal/visibility"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// The assessment activities are budgeted from the work they can actually do,
// not from one shared number. Both crawls issue their requests serially with
// each request capped at fetchSiteTimeout, so a request count times that cap is
// a real bound rather than a guess — and raising a request budget raises the
// matching timeout with it.
const (
	// siteAuditTimeout bounds one RunSiteAudit attempt. Unlike FetchSite, the
	// audit crawl puts no deadline on the crawl as a whole, so its worst case is
	// every request it may make: the page budget plus robots.txt.
	siteAuditTimeout = (fetchSiteMaxRequests + 1) * fetchSiteTimeout

	// sourceClassificationAllowance is the share of a RunFinders attempt left for
	// the batched classification call and its validation retry. Nothing enforces
	// it on its own; it is the headroom findersTimeout adds on top of research so
	// a slow model call is not charged against the fetch budget.
	sourceClassificationAllowance = 3 * time.Minute

	// findersTimeout bounds one RunFinders attempt: a fully spent research budget
	// of serial fetches, plus the classification call.
	findersTimeout = visibility.ResearchURLBudget*fetchSiteTimeout + sourceClassificationAllowance
)

type AssessmentWorkflowInput struct{ AccountID, BusinessID, RunID domain.ID }

// SiteAuditResult is what one crawl of the business site yields. The bounded
// page text crosses the activity boundary because finders compare cited
// competitor evidence with what the customer already publishes before asking
// them to add anything.
type SiteAuditResult struct {
	Checks      []visibility.CheckResult
	PagesRead   int
	Failure     string
	SiteContent string
}

// RunSiteAudit crawls the site once and evaluates the whole check catalog. A
// crawl that fails is a successful activity with every check unverifiable — a
// site we could not read is a fact about the site, not an error in the run.
func (a *Activities) RunSiteAudit(ctx context.Context, in AssessmentWorkflowInput) (SiteAuditResult, error) {
	business, err := a.Store.GetBusiness(ctx, in.AccountID, in.BusinessID)
	if err != nil {
		return SiteAuditResult{}, err
	}
	scan := visibility.OwnedSiteScan{PayloadVersion: visibility.OwnedSiteScanPayloadVersion, FetchFailure: "business website is not configured"}
	if business.Website != nil {
		fetcher := newSiteFetcher()
		scan = scanOwnedSite(ctx, fetcher, *business.Website)
		fetcher.closeIdleConnections()
	}
	scan.BusinessName = business.Name
	return SiteAuditResult{Checks: visibility.Audit(scan), PagesRead: len(scan.Pages), Failure: scan.FetchFailure, SiteContent: scan.Text}, nil
}

type FindImprovementsInput struct {
	AssessmentWorkflowInput
	Audit SiteAuditResult
}

// RunFinders turns the audit and the answer corpus into work. Every finder runs
// against the same input and shares one SSRF-safe research budget, so no finder
// can spend the whole allowance on its own.
//
// The budget is built here and so is per attempt, not per run: a retried attempt
// starts with a full allowance and re-reads pages the failed attempt already
// read. That is the honest cost of retrying an activity that caches nothing, and
// it is why RunFinders is given fewer attempts than the crawl beside it.
func (a *Activities) RunFinders(ctx context.Context, in FindImprovementsInput) ([]visibility.Finding, error) {
	snapshot, err := a.Store.LoadMonitoringSnapshot(ctx, in.AccountID, in.BusinessID)
	if err != nil {
		return nil, err
	}
	previous, err := a.Store.ListFindings(ctx, in.AccountID, in.BusinessID)
	if err != nil {
		return nil, err
	}
	priorContentGaps := []llm.PriorContentGap{}
	for _, finding := range previous {
		if finding.Source != visibility.SourceCompetitorContent {
			continue
		}
		key, ok := strings.CutPrefix(finding.Key, visibility.SourceCompetitorContent+":")
		if !ok || !llm.ValidContentGapKey(key) {
			continue
		}
		recommendation := ""
		if len(finding.Steps) > 0 {
			recommendation = finding.Steps[0]
		}
		priorContentGaps = append(priorContentGaps, llm.PriorContentGap{Key: key, Title: finding.Title, Recommendation: recommendation})
	}
	research := &boundedHTTPResearcher{client: newSafeFetchHTTPClient(), remaining: visibility.ResearchURLBudget}
	defer research.client.CloseIdleConnections()

	input := visibility.FinderInput{
		Audit: in.Audit.Checks, Snapshot: snapshot, SiteContent: in.Audit.SiteContent,
		PriorContentGaps: priorContentGaps, Classifier: a.SourceClassifier,
	}
	out := []visibility.Finding{}
	for _, finder := range visibility.Finders() {
		findings, err := finder.Find(ctx, input, research)
		if err != nil {
			if errors.Is(err, llm.ErrSourceClassificationValidation) {
				return nil, temporal.NewNonRetryableApplicationError("classify citation sources", "InvalidSourceClassification", err)
			}
			return nil, fmt.Errorf("finder %s: %w", finder.Key(), err)
		}
		out = append(out, findings...)
	}
	visibility.Rank(out)
	return out, nil
}

type PublishImproveRunInput struct {
	AssessmentWorkflowInput
	Audit    SiteAuditResult
	Findings []visibility.Finding
}

func (a *Activities) PublishImproveRun(ctx context.Context, in PublishImproveRunInput) error {
	return a.Store.PublishImproveRun(ctx, in.AccountID, in.BusinessID, store.ImproveRun{
		RunID:     in.RunID,
		PagesRead: in.Audit.PagesRead,
		Failure:   in.Audit.Failure,
		Checks:    in.Audit.Checks,
		Findings:  in.Findings,
	})
}

// AssessmentWorkflow audits the site, derives findings from it and the answer
// corpus, and publishes both as one unit.
//
// There is no partial-failure handling by design: an activity that fails fails
// the run, and the previously published audit and findings stay current. Half of
// this week's advice mixed with half of last week's would be harder to reason
// about than simply showing last week's until the next run succeeds.
func AssessmentWorkflow(ctx workflow.Context, in AssessmentWorkflowInput) error {
	// Each activity gets its own budget: one shared number can only ever fit the
	// smallest of the three.
	auditCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		// The crawl buys no model tokens, so a full three attempts stays cheap.
		StartToCloseTimeout: siteAuditTimeout,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 3},
	})

	var audit SiteAuditResult
	if err := workflow.ExecuteActivity(auditCtx, acts.RunSiteAudit, in).Get(ctx, &audit); err != nil {
		return err
	}

	findersCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: findersTimeout,
		// An attempt re-fetches every research page and re-pays for the
		// classification call from scratch, so attempts are expensive in a way
		// the crawl's are not. One retry covers a transient database or model
		// blip; past that, leaving the previous findings current is already the
		// designed outcome rather than a failure worth paying for again.
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 2},
	})

	var findings []visibility.Finding
	if err := workflow.ExecuteActivity(findersCtx, acts.RunFinders, FindImprovementsInput{
		AssessmentWorkflowInput: in, Audit: audit,
	}).Get(ctx, &findings); err != nil {
		return err
	}

	// One transaction, and the unique monitoring-run constraint makes a repeat
	// publication a no-op, so retrying it freely is safe.
	publishCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 3},
	})
	return workflow.ExecuteActivity(publishCtx, acts.PublishImproveRun, PublishImproveRunInput{
		AssessmentWorkflowInput: in, Audit: audit, Findings: findings,
	}).Get(ctx, nil)
}

// robotsReport is one inspection of robots.txt: the verdict for every crawler
// the checklist reports on, whether a file exists at all, and the sitemaps it
// declares.
type robotsReport struct {
	Found    bool
	Denied   map[string]bool
	Sitemaps []string
}

// scanOwnedSite inspects the business site once. The fetcher supplies the page
// text, the checked URLs, the page facts and the indexing directives it read
// while it had the markup in hand, so the only extra request the scan makes is
// robots.txt.
func scanOwnedSite(ctx context.Context, fetcher *siteFetcher, website string) visibility.OwnedSiteScan {
	scan := visibility.OwnedSiteScan{PayloadVersion: visibility.OwnedSiteScanPayloadVersion}
	u, err := url.Parse(website)
	if err != nil || u.Hostname() == "" {
		scan.FetchFailure = "invalid website URL"
		return scan
	}
	scan.Host = strings.ToLower(u.Hostname())

	out, err := fetcher.Fetch(ctx, FetchSiteInput{Website: website})
	switch {
	case err == nil:
		scan.Reachable = true
		scan.Text = out.Text
		scan.CheckedURLs = out.URLs
		scan.Pages = out.Pages
		scan.NoIndexURLs = out.NoIndexURLs
		scan.SitemapFound = out.SitemapFound
		// The homepage is the site's most linked and most cited page, so a
		// barrier there is a barrier for the site even when other pages are
		// public. Away from it one noindex page (a thank-you or search page) is
		// incidental, and only a directive on every page the fetcher read
		// counts.
		scan.AuthBarrier = out.HomeAuthBarrier
		scan.NoIndex = out.HomeNoIndex || len(out.NoIndexURLs) > 0 && len(out.NoIndexURLs) == len(out.URLs)
	case errors.Is(err, errAuthBarrier):
		// The fetch already saw the 401/403; the barrier is confirmed without
		// asking the site again.
		scan.AuthBarrier = true
		scan.CheckedURLs = []string{website}
	default:
		scan.FetchFailure = err.Error()
		return scan
	}

	robots := *u
	robots.Path = "/robots.txt"
	robots.RawQuery = ""
	report, robotsErr := inspectRobots(ctx, fetcher.client, robots.String(), checkedPaths(scan.CheckedURLs))
	scan.CheckedURLs = append(scan.CheckedURLs, robots.String())
	if robotsErr != nil {
		// Only the robots-derived checks lose their evidence here. Reachability
		// and the indexing directives were established from the pages
		// themselves, so they keep their verdicts.
		scan.RobotsFailure = robotsErr.Error()
		return scan
	}
	scan.RobotsFound = report.Found
	scan.AgentDenied = report.Denied
	scan.RobotsSitemaps = report.Sitemaps
	return scan
}

// inspectRobots reads robots.txt once and reports the verdict for every crawler
// the checklist names. A 4xx means the site publishes no robots file and
// therefore disallows nothing; a 5xx or a transport failure is an inspection
// failure the caller must not read as a pass.
func inspectRobots(ctx context.Context, client *http.Client, robotsURL string, paths []string) (robotsReport, error) {
	agents := []string{visibility.AgentOAISearchBot, visibility.AgentChatGPTUser, visibility.AgentGPTBot}
	allowAll := robotsReport{Denied: map[string]bool{}}
	for _, agent := range agents {
		allowAll.Denied[agent] = false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return robotsReport{}, err
	}
	req.Header.Set("Accept", "text/plain,*/*")
	req.Header.Set("User-Agent", fetchSiteUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return robotsReport{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
		if readErr != nil {
			return robotsReport{}, readErr
		}
		raw := string(body)
		report := robotsReport{Found: true, Denied: map[string]bool{}, Sitemaps: robotsSitemaps(raw)}
		for _, agent := range agents {
			report.Denied[agent] = robotsDenies(raw, agent, paths)
		}
		return report, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return allowAll, nil
	default:
		return robotsReport{}, fmt.Errorf("robots.txt returned status %d", resp.StatusCode)
	}
}

// robotsSitemaps returns the sitemap URLs robots.txt declares. The directive is
// global, so it is read outside any user-agent group.
func robotsSitemaps(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		key, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "sitemap") {
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// checkedPaths returns the paths the robots rules are evaluated against: the
// site root plus every page the fetcher actually read.
func checkedPaths(urls []string) []string {
	paths := []string{"/"}
	for _, raw := range urls {
		if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" && parsed.Path != "/" {
			paths = append(paths, parsed.Path)
		}
	}
	return paths
}

// robotsDenies reports whether robots.txt denies the given crawler any of the
// given page paths. The agent must already be lowercased.
func robotsDenies(raw, agent string, paths []string) bool {
	type rule struct {
		allow bool
		path  string
	}
	rules := map[string][]rule{}
	agents := []string{}
	sawRule := false
	for _, line := range strings.Split(strings.ToLower(raw), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if key == "user-agent" {
			if sawRule {
				agents = nil
				sawRule = false
			}
			agents = append(agents, value)
			continue
		}
		if key != "allow" && key != "disallow" {
			continue
		}
		sawRule = true
		// An empty "Disallow:" means the group allows everything, so record it
		// as the least specific allow rule: the group then counts as having
		// rules (rather than falling back to the wildcard group) while any
		// longer rule in the same group still wins. An empty "Allow:" says
		// nothing.
		if value == "" {
			if key != "disallow" {
				continue
			}
			key, value = "allow", "/"
		}
		for _, agent := range agents {
			rules[agent] = append(rules[agent], rule{allow: key == "allow", path: value})
		}
	}
	selected := rules[agent]
	if len(selected) == 0 {
		selected = rules["*"]
	}
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	for _, path := range paths {
		// The matching subject is the page path and a rule value is a path
		// prefix, so the page is the HasPrefix argument, not the rule. The
		// longest matching rule wins and Allow wins an equal-length tie.
		matched := -1
		allowed := true
		for _, candidate := range selected {
			if strings.HasPrefix(strings.ToLower(path), candidate.path) && (len(candidate.path) > matched || len(candidate.path) == matched && candidate.allow) {
				matched = len(candidate.path)
				allowed = candidate.allow
			}
		}
		if matched >= 0 && !allowed {
			return true
		}
	}
	return false
}

type boundedHTTPResearcher struct {
	client    *http.Client
	remaining int
}

func (r *boundedHTTPResearcher) Inspect(ctx context.Context, in visibility.ResearchRequest) (visibility.ResearchResult, error) {
	if r.remaining <= 0 {
		return visibility.ResearchResult{}, visibility.ErrResearchBudget
	}
	r.remaining--
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Hostname() == "" {
		return visibility.ResearchResult{}, errors.New("invalid research URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return visibility.ResearchResult{}, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return visibility.ResearchResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return visibility.ResearchResult{}, fmt.Errorf("source returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return visibility.ResearchResult{}, err
	}
	return visibility.ResearchResult{URL: resp.Request.URL.String(), Text: string(body), CheckedAt: time.Now().UTC()}, nil
}
