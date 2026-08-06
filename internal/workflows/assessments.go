package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"opensight/internal/domain"
	"opensight/internal/store"
	"opensight/internal/visibility"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"golang.org/x/net/html"
)

type AssessmentWorkflowInput struct{ AccountID, BusinessID, RunID domain.ID }
type AssessmentPlan struct {
	GenerationID domain.ID
	Entries      []store.ModulePlanEntry
}

func (a *Activities) ResolveAssessmentPlan(ctx context.Context, in AssessmentWorkflowInput) (AssessmentPlan, error) {
	assessors := visibility.Assessors(a.AssessmentModes)
	entries := make([]store.ModulePlanEntry, 0, len(assessors))
	for _, assessor := range assessors {
		m := assessor.Manifest()
		if m.Mode == visibility.RolloutDisabled {
			continue
		}
		for _, key := range m.PracticeKeys {
			p, ok := visibility.Practice(key)
			if !ok || p.AssessorKey != m.Key {
				return AssessmentPlan{}, temporal.NewNonRetryableApplicationError("invalid visibility registry", "BadVisibilityRegistry", nil)
			}
		}
		entries = append(entries, store.ModulePlanEntry{AssessorKey: m.Key, ModuleVersion: m.ModuleVersion, Mode: m.Mode, PracticeKeys: m.PracticeKeys, RequiredCollectors: m.RequiredCollectors})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].AssessorKey < entries[j].AssessorKey })
	generation, err := a.Store.StartAssessmentGeneration(ctx, in.AccountID, in.BusinessID, in.RunID, entries)
	if err != nil {
		return AssessmentPlan{}, err
	}
	return AssessmentPlan{GenerationID: generation.ID, Entries: entries}, nil
}

type CollectEvidenceInput struct {
	AssessmentWorkflowInput
	GenerationID domain.ID
	CollectorKey string
}

func (a *Activities) CollectAssessmentEvidence(ctx context.Context, in CollectEvidenceInput) (visibility.EvidenceArtifact, error) {
	artifact := visibility.EvidenceArtifact{CollectorKey: in.CollectorKey, CollectorVersion: 1, PayloadVersion: 1, CheckedAt: time.Now().UTC()}
	var collectErr error
	switch in.CollectorKey {
	case visibility.CollectorMonitoring:
		snap, err := a.Store.LoadMonitoringSnapshot(ctx, in.AccountID, in.BusinessID)
		collectErr = err
		if err == nil {
			artifact.Payload, err = json.Marshal(snap)
			collectErr = err
		}
	case visibility.CollectorOwnedSite:
		business, err := a.Store.GetBusiness(ctx, in.AccountID, in.BusinessID)
		if err != nil {
			collectErr = err
			break
		}
		scan := visibility.OwnedSiteScan{PayloadVersion: 1}
		if business.Website == nil {
			scan.Failure = "business website is not configured"
		} else {
			scan = scanOwnedSite(ctx, *business.Website)
		}
		artifact.Payload, err = json.Marshal(scan)
		collectErr = err
	default:
		collectErr = fmt.Errorf("unknown collector %q", in.CollectorKey)
	}
	if err := a.Store.SaveEvidenceArtifact(ctx, in.GenerationID, in.AccountID, artifact, collectErr); err != nil {
		return visibility.EvidenceArtifact{}, err
	}
	if collectErr != nil {
		return visibility.EvidenceArtifact{}, collectErr
	}
	return artifact, nil
}

func scanOwnedSite(ctx context.Context, website string) visibility.OwnedSiteScan {
	scan := visibility.OwnedSiteScan{PayloadVersion: 1}
	u, err := url.Parse(website)
	if err != nil || u.Hostname() == "" {
		scan.Failure = "invalid website URL"
		return scan
	}
	scan.Host = strings.ToLower(u.Hostname())
	client := newSafeFetchHTTPClient()
	defer client.CloseIdleConnections()
	fetcher := newSiteFetcher()
	defer fetcher.closeIdleConnections()
	out, err := fetcher.Fetch(ctx, FetchSiteInput{Website: website})
	if err != nil {
		rootReq, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, website, nil)
		if requestErr != nil {
			scan.Failure = requestErr.Error()
			return scan
		}
		resp, requestErr := client.Do(rootReq)
		if requestErr != nil {
			scan.Failure = err.Error()
			return scan
		}
		scan.CheckedURLs = append(scan.CheckedURLs, resp.Request.URL.String())
		scan.AuthBarrier = resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
		_ = resp.Body.Close()
		if !scan.AuthBarrier {
			scan.Failure = err.Error()
			return scan
		}
	} else {
		scan.Reachable = true
		scan.Text = out.Text
		scan.CheckedURLs = out.URLs
		sum := sha256.Sum256([]byte(out.Text))
		scan.Hashes = []string{hex.EncodeToString(sum[:])}
		scan.Summaries = []string{truncateText(out.Text, 500)}
	}
	rootReq, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, website, nil)
	if requestErr == nil {
		if resp, e := client.Do(rootReq); e == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
			_ = resp.Body.Close()
			scan.AuthBarrier = resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
			scan.NoIndex = strings.Contains(strings.ToLower(resp.Header.Get("X-Robots-Tag")), "noindex") || htmlNoIndex(body)
		}
	}
	robots := *u
	robots.Path = "/robots.txt"
	robots.RawQuery = ""
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, robots.String(), nil)
	if resp, e := client.Do(req); e == nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
		_ = resp.Body.Close()
		scan.CheckedURLs = append(scan.CheckedURLs, robots.String())
		scan.OAIBlocked = robotsDeniesOAI(string(body))
	}
	return scan
}
func htmlNoIndex(body []byte) bool {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return false
	}
	var visit func(*html.Node) bool
	visit = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "meta" {
			name, content := "", ""
			for _, attr := range n.Attr {
				switch strings.ToLower(attr.Key) {
				case "name":
					name = strings.ToLower(strings.TrimSpace(attr.Val))
				case "content":
					content = strings.ToLower(attr.Val)
				}
			}
			if (name == "robots" || name == "oai-searchbot") && strings.Contains(content, "noindex") {
				return true
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if visit(child) {
				return true
			}
		}
		return false
	}
	return visit(doc)
}

func robotsDeniesOAI(raw string) bool {
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
		if value == "" {
			continue
		}
		for _, agent := range agents {
			rules[agent] = append(rules[agent], rule{allow: key == "allow", path: value})
		}
	}
	selected := rules["oai-searchbot"]
	if len(selected) == 0 {
		selected = rules["*"]
	}
	matched := -1
	allowed := true
	for _, candidate := range selected {
		if strings.HasPrefix("/", candidate.path) && (len(candidate.path) > matched || len(candidate.path) == matched && candidate.allow) {
			matched = len(candidate.path)
			allowed = candidate.allow
		}
	}
	return matched >= 0 && !allowed
}
func truncateText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

type boundedHTTPResearcher struct {
	client    *http.Client
	remaining int
}

func (r *boundedHTTPResearcher) Inspect(ctx context.Context, in visibility.ResearchRequest) (visibility.ResearchResult, error) {
	if r.remaining <= 0 {
		return visibility.ResearchResult{}, errors.New("research URL budget exhausted")
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

type RunAssessorInput struct {
	AssessmentWorkflowInput
	GenerationID domain.ID
	Entry        store.ModulePlanEntry
	Artifacts    []visibility.EvidenceArtifact
}
type RunAssessorOutput struct {
	Drafts        []visibility.AssessmentDraft
	AssessmentIDs []domain.ID
}

func (a *Activities) RunPracticeAssessor(ctx context.Context, in RunAssessorInput) (RunAssessorOutput, error) {
	var selected visibility.PracticeAssessor
	for _, candidate := range visibility.Assessors(a.AssessmentModes) {
		if candidate.Manifest().Key == in.Entry.AssessorKey && candidate.Manifest().ModuleVersion == in.Entry.ModuleVersion {
			selected = candidate
			break
		}
	}
	if selected == nil {
		return RunAssessorOutput{}, temporal.NewNonRetryableApplicationError("assessor not registered", "BadVisibilityRegistry", nil)
	}
	m := selected.Manifest()
	research := &boundedHTTPResearcher{client: newSafeFetchHTTPClient(), remaining: m.MaxURLInspections}
	defer research.client.CloseIdleConnections()
	drafts, err := selected.Assess(ctx, visibility.NewEvidenceView(in.Artifacts), research)
	if err != nil {
		return RunAssessorOutput{}, err
	}
	if len(drafts) > m.MaxOutput {
		return RunAssessorOutput{}, temporal.NewNonRetryableApplicationError("assessor output exceeds limit", "BadAssessorOutput", nil)
	}
	out := RunAssessorOutput{Drafts: drafts}
	for _, d := range drafts {
		if err := visibility.ValidateDraft(d, m); err != nil {
			return RunAssessorOutput{}, temporal.NewNonRetryableApplicationError("invalid assessment", "BadAssessorOutput", err)
		}
		id, err := a.Store.SaveAssessment(ctx, in.GenerationID, in.AccountID, in.BusinessID, d, in.Entry.Mode)
		if err != nil {
			return RunAssessorOutput{}, err
		}
		out.AssessmentIDs = append(out.AssessmentIDs, id)
	}
	return out, nil
}

type CompileOpportunitiesInput struct {
	AssessmentWorkflowInput
	GenerationID domain.ID
	Outputs      []RunAssessorOutput
	Entries      []store.ModulePlanEntry
	HadFailure   bool
}

func (a *Activities) CompileOpportunities(ctx context.Context, in CompileOpportunitiesInput) error {
	modes := map[string]visibility.RolloutMode{}
	for _, e := range in.Entries {
		modes[e.AssessorKey] = e.Mode
	}
	drafts := []visibility.AssessmentDraft{}
	ids := map[string]domain.ID{}
	for _, o := range in.Outputs {
		for i, d := range o.Drafts {
			drafts = append(drafts, d)
			if i < len(o.AssessmentIDs) {
				ids[d.PracticeKey+"\x00"+d.SubjectKey] = o.AssessmentIDs[i]
			}
		}
	}
	compiled, err := visibility.Compile(drafts, modes)
	if err != nil {
		_ = a.Store.FinishAssessmentGeneration(ctx, in.GenerationID, in.AccountID, "FAILED", err)
		return err
	}
	presenters := visibility.Presenters()
	for _, draft := range drafts {
		if modes[draft.AssessorKey] != visibility.RolloutActive {
			continue
		}
		presenter := presenters[draft.AssessorKey]
		if presenter == nil {
			continue
		}
		presentation, presentErr := presenter.Present(draft)
		if presentErr != nil {
			continue
		}
		_ = a.Store.RefreshExistingOpportunity(ctx, in.AccountID, in.BusinessID, ids[draft.PracticeKey+"\x00"+draft.SubjectKey], draft, presentation)
	}
	for i, item := range compiled {
		id := ids[item.Draft.PracticeKey+"\x00"+item.Draft.SubjectKey]
		if _, err := a.Store.SaveOpportunity(ctx, in.AccountID, in.BusinessID, id, i+1, item.Draft, item.Presentation); err != nil {
			return err
		}
	}
	candidates, err := a.Store.ListCompletedOutcomeCandidates(ctx, in.GenerationID, in.AccountID, in.BusinessID)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		practice, ok := visibility.Practice(candidate.PracticeKey)
		if !ok {
			continue
		}
		evaluator := visibility.Evaluators()[practice.EvaluatorKey]
		if evaluator == nil {
			continue
		}
		observations, evaluateErr := evaluator.Evaluate(ctx, visibility.Opportunity{ID: candidate.ID.String(), PracticeKey: candidate.PracticeKey, SubjectKey: candidate.SubjectKey, Assessment: candidate.Assessment}, visibility.NewEvidenceView(nil))
		if evaluateErr != nil {
			continue
		}
		for _, observation := range observations {
			key := fmt.Sprintf("outcome:%s:%s", in.GenerationID, observation.Key)
			_ = a.Store.AppendOutcomeObservation(ctx, in.AccountID, candidate.ID, key, observation.Payload)
		}
	}
	status := "READY"
	if in.HadFailure {
		status = "PARTIAL"
	}
	return a.Store.FinishAssessmentGeneration(ctx, in.GenerationID, in.AccountID, status, nil)
}

func AssessmentWorkflow(ctx workflow.Context, in AssessmentWorkflowInput) error {
	activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 3}})
	var plan AssessmentPlan
	if err := workflow.ExecuteActivity(activityCtx, acts.ResolveAssessmentPlan, in).Get(ctx, &plan); err != nil {
		return err
	}
	collectorSet := map[string]bool{}
	for _, entry := range plan.Entries {
		for _, key := range entry.RequiredCollectors {
			collectorSet[key] = true
		}
	}
	collectorKeys := make([]string, 0, len(collectorSet))
	for key := range collectorSet {
		collectorKeys = append(collectorKeys, key)
	}
	sort.Strings(collectorKeys)
	type pending struct {
		key    string
		future workflow.Future
	}
	pendingCollectors := []pending{}
	for _, key := range collectorKeys {
		pendingCollectors = append(pendingCollectors, pending{key, workflow.ExecuteActivity(activityCtx, acts.CollectAssessmentEvidence, CollectEvidenceInput{AssessmentWorkflowInput: in, GenerationID: plan.GenerationID, CollectorKey: key})})
	}
	artifacts := map[string]visibility.EvidenceArtifact{}
	failed := false
	for _, p := range pendingCollectors {
		var artifact visibility.EvidenceArtifact
		if err := p.future.Get(ctx, &artifact); err != nil {
			failed = true
			workflow.GetLogger(ctx).Error("evidence collector failed", "collector", p.key, "error", err.Error())
			continue
		}
		artifacts[p.key] = artifact
	}
	outputs := []RunAssessorOutput{}
	for _, entry := range plan.Entries {
		available := true
		deps := make([]visibility.EvidenceArtifact, 0, len(entry.RequiredCollectors))
		for _, key := range entry.RequiredCollectors {
			artifact, ok := artifacts[key]
			if !ok {
				available = false
				break
			}
			deps = append(deps, artifact)
		}
		if !available {
			failed = true
			continue
		}
		assessorCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 2}})
		var out RunAssessorOutput
		if err := workflow.ExecuteActivity(assessorCtx, acts.RunPracticeAssessor, RunAssessorInput{AssessmentWorkflowInput: in, GenerationID: plan.GenerationID, Entry: entry, Artifacts: deps}).Get(ctx, &out); err != nil {
			failed = true
			workflow.GetLogger(ctx).Error("practice assessor failed", "assessor", entry.AssessorKey, "error", err.Error())
			continue
		}
		outputs = append(outputs, out)
	}
	return workflow.ExecuteActivity(activityCtx, acts.CompileOpportunities, CompileOpportunitiesInput{AssessmentWorkflowInput: in, GenerationID: plan.GenerationID, Outputs: outputs, Entries: plan.Entries, HadFailure: failed}).Get(ctx, nil)
}
