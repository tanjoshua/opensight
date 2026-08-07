package visibility

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	CollectorMonitoring = "monitoring-snapshot"
	CollectorOwnedSite  = "owned-site-scan"
	RankerVersion       = 1
	CompilerVersion     = 1
)

type MonitoringSnapshot struct {
	PayloadVersion int           `json:"payload_version"`
	BusinessName   string        `json:"business_name"`
	WebsiteHost    string        `json:"website_host"`
	Runs           []SnapshotRun `json:"runs"`
}
type SnapshotRun struct {
	RunID   string           `json:"run_id"`
	Results []SnapshotResult `json:"results"`
}
type SnapshotResult struct {
	ResultID string `json:"result_id"`
	PromptID string `json:"prompt_id"`
	Prompt   string `json:"prompt"`
	// ResponseText is the only unrecoverable field in the payload: mentions and
	// citations are extractions of it, so an assessor that reads answers can
	// only ever be replayed over history that kept the answers.
	ResponseText    string   `json:"response_text"`
	Mentioned       bool     `json:"mentioned"`
	Competitors     []string `json:"competitors"`
	CitationDomains []string `json:"citation_domains"`
	CitationURLs    []string `json:"citation_urls"`
}
type OwnedSiteScan struct {
	PayloadVersion int      `json:"payload_version"`
	Host           string   `json:"host"`
	CheckedURLs    []string `json:"checked_urls"`
	Text           string   `json:"text"`
	Reachable      bool     `json:"reachable"`
	AuthBarrier    bool     `json:"auth_barrier"`
	NoIndex        bool     `json:"noindex"`
	OAIBlocked     bool     `json:"oai_blocked"`
	Failure        string   `json:"failure,omitempty"`
}

type assessFunc func(context.Context, EvidenceView, BoundedResearcher, AssessorManifest) ([]AssessmentDraft, error)

type staticAssessor struct {
	manifest AssessorManifest
	assess   assessFunc
}

func (a staticAssessor) Manifest() AssessorManifest { return a.manifest }
func (a staticAssessor) Assess(ctx context.Context, evidence EvidenceView, research BoundedResearcher) ([]AssessmentDraft, error) {
	return a.assess(ctx, evidence, research, a.manifest)
}

// assessors lists every implemented assessor, registered or not, next to the
// function that runs it — so adding one is a single entry here rather than an
// entry plus an arm in a dispatch switch.
//
// The budgets are calibrated and load-bearing — assessAuthority and assessTopics
// stop emitting at MaxOutput, so a zero there silently returns nothing — which
// is why an unregistered assessor keeps its manifest instead of losing it along
// with its registration.
var assessors = []staticAssessor{
	{AssessorManifest{Key: "search-access", ModuleVersion: 1, PracticeKeys: []string{PracticeSearchAccess}, RequiredCollectors: []string{CollectorOwnedSite}, MaxURLInspections: 2, MaxRuntime: 30 * time.Second, MaxOutput: 1}, assessSearchAccess},
	{AssessorManifest{Key: "influential-source", ModuleVersion: 1, PracticeKeys: []string{PracticeAuthority}, RequiredCollectors: []string{CollectorMonitoring}, MaxURLInspections: 5, MaxRuntime: 45 * time.Second, MaxOutput: 10}, assessAuthority},
	{AssessorManifest{Key: "tracked-topic", ModuleVersion: 1, PracticeKeys: []string{PracticeTopicCoverage}, RequiredCollectors: []string{CollectorMonitoring, CollectorOwnedSite}, MaxRuntime: 10 * time.Second, MaxOutput: 10}, assessTopics},
}

// registeredAssessors names the assessors that run in production. The other two
// are implemented and exercised by the fixture quality gate but never assessed,
// so their catalog entries stay UNKNOWN. Promotion is one entry here: the
// assessor starts running, and its fixture misses start failing the build.
var registeredAssessors = []string{"search-access"}

func registered(key string) bool { return slices.Contains(registeredAssessors, key) }

func Assessors() []PracticeAssessor {
	out := make([]PracticeAssessor, 0, len(registeredAssessors))
	for _, a := range assessors {
		if registered(a.manifest.Key) {
			out = append(out, a)
		}
	}
	return out
}

func decodeArtifact[T any](v EvidenceView, key string, version int) (T, error) {
	var out T
	a, ok := v.Artifact(key)
	if !ok {
		return out, fmt.Errorf("missing collector %s", key)
	}
	if a.PayloadVersion != version {
		return out, fmt.Errorf("unsupported %s payload version %d", key, a.PayloadVersion)
	}
	if err := json.Unmarshal(a.Payload, &out); err != nil {
		return out, err
	}
	return out, nil
}
func payload(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func assessSearchAccess(_ context.Context, v EvidenceView, _ BoundedResearcher, m AssessorManifest) ([]AssessmentDraft, error) {
	s, err := decodeArtifact[OwnedSiteScan](v, CollectorOwnedSite, 1)
	if err != nil {
		return nil, err
	}
	d := AssessmentDraft{PracticeKey: PracticeSearchAccess, CriteriaVersion: 1, AssessorKey: m.Key, AssessorVersion: m.ModuleVersion, SubjectKey: BusinessSubjectKey, Reach: 100, Persistence: 100, EvidenceQuality: 100, Actionability: 100, Effort: 2, PayloadVersion: 1}
	switch {
	case s.Failure != "":
		d.Status = StatusUnknown
		d.Explanation = "The website could not be inspected reliably: " + s.Failure
	case s.AuthBarrier || s.NoIndex || s.OAIBlocked || !s.Reachable:
		d.Status = StatusNotMet
		d.Explanation = "A confirmed access or indexing barrier prevents OpenAI search from retrieving the public site."
	default:
		d.Status = StatusMet
		d.Explanation = "The checked public pages were reachable, indexable, and not denied to OAI-SearchBot."
	}
	d.CheckedSources = append(d.CheckedSources, s.CheckedURLs...)
	d.Payload = payload(map[string]any{"reachable": s.Reachable, "auth_barrier": s.AuthBarrier, "noindex": s.NoIndex, "oai_blocked": s.OAIBlocked})
	return []AssessmentDraft{d}, nil
}

func assessAuthority(ctx context.Context, v EvidenceView, research BoundedResearcher, m AssessorManifest) ([]AssessmentDraft, error) {
	s, err := decodeArtifact[MonitoringSnapshot](v, CollectorMonitoring, 1)
	if err != nil {
		return nil, err
	}
	type agg struct {
		prompts, runs              map[string]bool
		results, urls, competitors []string
	}
	by := map[string]*agg{}
	for _, run := range s.Runs {
		for _, r := range run.Results {
			if r.Mentioned {
				continue
			}
			for _, domain := range r.CitationDomains {
				domain = strings.ToLower(domain)
				if domain == "" || domain == s.WebsiteHost {
					continue
				}
				x := by[domain]
				if x == nil {
					x = &agg{map[string]bool{}, map[string]bool{}, nil, nil, nil}
					by[domain] = x
				}
				x.prompts[r.PromptID] = true
				x.runs[run.RunID] = true
				x.results = append(x.results, r.ResultID)
				x.competitors = append(x.competitors, r.Competitors...)
				for _, rawURL := range r.CitationURLs {
					if parsed, parseErr := url.Parse(rawURL); parseErr == nil && strings.EqualFold(parsed.Hostname(), domain) {
						x.urls = append(x.urls, rawURL)
					}
				}
			}
		}
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []AssessmentDraft{}
	for _, domain := range keys {
		x := by[domain]
		recurring := len(x.prompts) >= 2 || (len(x.prompts) == 1 && len(x.runs) >= 2)
		if !recurring {
			continue
		}
		d := AssessmentDraft{PracticeKey: PracticeAuthority, CriteriaVersion: 1, AssessorKey: m.Key, AssessorVersion: m.ModuleVersion, SubjectKey: domain, Status: StatusUnknown, Explanation: "This source recurs in affected answers, but business presence could not be verified reliably.", ResultIDs: dedupe(x.results), Reach: len(x.prompts), Persistence: len(x.runs), EvidenceQuality: 2, Actionability: 2, Effort: 2, PayloadVersion: 1}
		found := false
		for _, raw := range dedupe(x.urls) {
			if research == nil {
				break
			}
			rr, e := research.Inspect(ctx, ResearchRequest{URL: raw})
			if e != nil {
				continue
			}
			d.CheckedSources = append(d.CheckedSources, rr.URL)
			if containsName(rr.Text, s.BusinessName) {
				found = true
				break
			}
		}
		if found {
			d.Status = StatusMet
			d.Explanation = "The business is present on this recurring influential source."
		} else if len(d.CheckedSources) > 0 && len(x.competitors) > 0 {
			d.Status = StatusNotMet
			d.Explanation = "Competitors appear in affected answers using this recurring source, while the inspected relevant pages do not mention the business."
		}
		d.Payload = payload(map[string]any{"domain": domain, "affected_questions": len(x.prompts), "recurring_runs": len(x.runs)})
		out = append(out, d)
		if len(out) >= m.MaxOutput {
			break
		}
	}
	return out, nil
}

func assessTopics(_ context.Context, v EvidenceView, _ BoundedResearcher, m AssessorManifest) ([]AssessmentDraft, error) {
	mon, err := decodeArtifact[MonitoringSnapshot](v, CollectorMonitoring, 1)
	if err != nil {
		return nil, err
	}
	site, err := decodeArtifact[OwnedSiteScan](v, CollectorOwnedSite, 1)
	if err != nil {
		return nil, err
	}
	type topic struct {
		prompt   string
		promptID string
		results  map[string]bool
		runs     map[string]bool
	}
	topics := map[string]*topic{}
	for _, run := range mon.Runs {
		for _, r := range run.Results {
			if r.Mentioned {
				continue
			}
			key := topicKey(r.Prompt)
			if key == "" {
				continue
			}
			t := topics[key]
			if t == nil {
				t = &topic{r.Prompt, r.PromptID, map[string]bool{}, map[string]bool{}}
				topics[key] = t
			}
			t.results[r.ResultID] = true
			t.runs[run.RunID] = true
		}
	}
	keys := make([]string, 0, len(topics))
	for k := range topics {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []AssessmentDraft{}
	for _, key := range keys {
		t := topics[key]
		if len(t.results) < 2 && len(t.runs) < 2 {
			continue
		}
		d := AssessmentDraft{PracticeKey: PracticeTopicCoverage, CriteriaVersion: 1, AssessorKey: m.Key, AssessorVersion: m.ModuleVersion, SubjectKey: key, PromptIDs: []string{t.promptID}, ResultIDs: mapKeys(t.results), Reach: len(t.results), Persistence: len(t.runs), EvidenceQuality: 2, Actionability: 3, Effort: 2, PayloadVersion: 1}
		switch {
		case site.Failure != "":
			d.Status = StatusUnknown
			d.Explanation = "Owned content could not be assessed reliably."
		case topicCovered(site.Text, key):
			d.Status = StatusMet
			d.Explanation = "Accessible owned content clearly covers this tracked customer need."
		case strings.Contains(strings.ToLower(site.Text), strings.Fields(key)[0]):
			d.Status = StatusPartial
			d.Explanation = "The topic is mentioned on the site but important details are difficult to find or incomplete."
		default:
			d.Status = StatusNotMet
			d.Explanation = "No clear accessible owned coverage was found for this recurring customer need."
		}
		d.CheckedSources = site.CheckedURLs
		d.Payload = payload(map[string]any{"topic": key, "question": t.prompt})
		out = append(out, d)
		if len(out) >= m.MaxOutput {
			break
		}
	}
	return out, nil
}

func containsName(text, name string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(strings.TrimSpace(name)))
}
func topicKey(prompt string) string {
	words := strings.Fields(strings.ToLower(prompt))
	stop := map[string]bool{"what": true, "which": true, "where": true, "who": true, "are": true, "is": true, "the": true, "a": true, "an": true, "for": true, "in": true, "near": true, "best": true, "recommend": true, "recommended": true, "me": true}
	out := []string{}
	for _, w := range words {
		w = strings.Trim(w, ".,?!:;()[]\"")
		if len(w) > 3 && !stop[w] {
			out = append(out, w)
		}
		if len(out) == 3 {
			break
		}
	}
	return strings.Join(out, " ")
}
func topicCovered(text, key string) bool {
	lower := strings.ToLower(text)
	words := strings.Fields(key)
	hits := 0
	for _, w := range words {
		if strings.Contains(lower, w) {
			hits++
		}
	}
	return len(words) > 0 && hits >= min(2, len(words))
}
func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// catalogPresenter renders any practice from its catalog entry, so a new
// practice needs no presenter code. It is the only Presenter; the interface
// stays as the seam for a practice that eventually needs bespoke rendering.
type catalogPresenter struct{}

func (catalogPresenter) Present(d AssessmentDraft) (Presentation, error) {
	def, ok := Practice(d.PracticeKey)
	if !ok {
		return Presentation{}, fmt.Errorf("unknown practice %q", d.PracticeKey)
	}
	effort := []string{"", "Small", "Small", "Medium", "Large"}[min(4, max(1, d.Effort))]
	title := def.Title
	if def.SubjectInTitle {
		title += ": " + d.SubjectKey
	}
	blocks := []PresentationBlock{{Type: BlockText, Title: "Why this is showing", Text: d.Explanation}}
	blocks = append(blocks, PresentationBlock{Type: BlockQuestionList, Title: "Suggested steps", Items: def.Steps})
	if len(d.ResultIDs) > 0 {
		blocks = append(blocks, PresentationBlock{Type: BlockMetric, Title: "Affected responses", Value: fmt.Sprint(len(d.ResultIDs)), ResultIDs: d.ResultIDs})
	}
	if len(d.CheckedSources) > 0 {
		for _, u := range d.CheckedSources {
			if parsed, e := url.Parse(u); e == nil && parsed.IsAbs() {
				blocks = append(blocks, PresentationBlock{Type: BlockLink, Title: "Checked source", URL: u})
			}
		}
	}
	blocks = append(blocks, PresentationBlock{Type: BlockNotice, Text: "OpenSight can observe later changes, but cannot prove that one action caused a ranking change."})
	blocks = append(blocks, PresentationBlock{Type: BlockNotice, Text: "For regulated services, have a qualified reviewer confirm every claim before publishing."})
	pres := Presentation{Title: title, Summary: d.Explanation, Effort: effort, Blocks: blocks}
	return pres, ValidatePresentation(pres)
}

// CompiledAssessment is one eligible assessment paired with the presentation
// the compiler rendered for it. Its position in Compile's result is its rank.
type CompiledAssessment struct {
	Draft        AssessmentDraft
	Presentation Presentation
}

func Compile(drafts []AssessmentDraft) ([]CompiledAssessment, error) {
	eligible := []AssessmentDraft{}
	for _, d := range drafts {
		if Eligible(d) {
			eligible = append(eligible, d)
		}
	}
	Rank(eligible)
	// Practices are described entirely by their catalog entry, so one presenter
	// serves all of them; the interface stays the compiler's rendering seam.
	var presenter Presenter = catalogPresenter{}
	out := make([]CompiledAssessment, 0, len(eligible))
	for _, d := range eligible {
		pres, err := presenter.Present(d)
		if err != nil {
			return nil, err
		}
		out = append(out, CompiledAssessment{Draft: d, Presentation: pres})
	}
	return out, nil
}
