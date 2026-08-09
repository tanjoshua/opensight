package visibility

import (
	"context"
	"strings"
	"testing"

	"opensight/internal/llm"
)

type fixedSourceClassifier struct {
	kind        string
	owner       string
	suppressGap bool
	calls       int
	in          llm.SourceClassificationInput
}

func (c *fixedSourceClassifier) ClassifySources(_ context.Context, in llm.SourceClassificationInput) (llm.SourceAnalysis, error) {
	c.calls++
	c.in = in
	out := make([]llm.SourceClassification, len(in.Candidates))
	evidence := []llm.SourceClaimReference{}
	for i, candidate := range in.Candidates {
		out[i] = llm.SourceClassification{CandidateIndex: i, Kind: c.kind, ClaimIndices: []int{}}
		if c.kind == llm.SourceCompetitorOwned {
			owner := c.owner
			if owner == "" && len(candidate.Claims) > 0 {
				owner = candidate.Claims[0].Owner
			}
			out[i].Owner = owner
			for j, claim := range candidate.Claims {
				if claim.Owner == owner {
					out[i].ClaimIndices = append(out[i].ClaimIndices, j)
					evidence = append(evidence, llm.SourceClaimReference{CandidateIndex: i, ClaimIndex: j})
				}
			}
		}
	}
	analysis := llm.SourceAnalysis{Sources: out, Gaps: []llm.ContentGap{}}
	if c.kind == llm.SourceCompetitorOwned && !c.suppressGap {
		analysis.Gaps = []llm.ContentGap{{
			Topic: llm.ContentTopicServices, Title: "Explain your complex-case services",
			Reason:         "Your site does not clearly describe these capabilities.",
			Recommendation: "On your services page, state which complex cases you treat, if offered.",
			Coverage:       "absent", SiteEvidence: []string{}, Evidence: evidence,
		}}
	}
	return analysis, nil
}

func TestCoveredCompetitorContentProducesNoAction(t *testing.T) {
	classifier := &fixedSourceClassifier{kind: llm.SourceCompetitorOwned, owner: "Rival Clinic", suppressGap: true}
	research := &fixtureResearcher{remaining: ResearchURLBudget, pages: map[string]string{
		"https://www.rival.example/a": "Rival Clinic", "https://www.rival.example/b": "Rival Clinic",
	}}
	findings, err := citationGapFinder{}.Find(context.Background(), FinderInput{
		Snapshot: linkedSnapshot(), SiteContent: "We offer microscope-assisted treatment and handle complex cases.", Classifier: classifier,
	}, research)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("already-covered competitor content produced findings: %+v", findings)
	}
}

func TestCompetitorContentGroupsDomainsByActionableTopic(t *testing.T) {
	snapshot := MonitoringSnapshot{BusinessName: "Customer Clinic", Runs: []SnapshotRun{{RunID: "run-1", Results: []SnapshotResult{
		{ResultID: "a-1", PromptID: "p-1", Citations: []SnapshotCitation{{URL: "https://alpha.example/team", Domain: "alpha.example", Passage: "Alpha Clinic publishes each specialist's credentials.", Competitors: []string{"Alpha Clinic"}}}},
		{ResultID: "a-2", PromptID: "p-2", Citations: []SnapshotCitation{{URL: "https://alpha.example/about", Domain: "alpha.example", Passage: "Alpha Clinic explains its specialist experience.", Competitors: []string{"Alpha Clinic"}}}},
		{ResultID: "b-1", PromptID: "p-3", Citations: []SnapshotCitation{{URL: "https://beta.example/team", Domain: "beta.example", Passage: "Beta Clinic lists postgraduate qualifications.", Competitors: []string{"Beta Clinic"}}}},
		{ResultID: "b-2", PromptID: "p-4", Citations: []SnapshotCitation{{URL: "https://beta.example/about", Domain: "beta.example", Passage: "Beta Clinic names its specialist registrations.", Competitors: []string{"Beta Clinic"}}}},
	}}}}
	classifier := &fixedSourceClassifier{kind: llm.SourceCompetitorOwned}
	research := &fixtureResearcher{remaining: ResearchURLBudget, pages: map[string]string{
		"https://alpha.example/team": "Alpha Clinic", "https://alpha.example/about": "Alpha Clinic",
		"https://beta.example/team": "Beta Clinic", "https://beta.example/about": "Beta Clinic",
	}}

	findings, err := citationGapFinder{}.Find(context.Background(), FinderInput{Snapshot: snapshot, Classifier: classifier}, research)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Key != "competitor-content:services_capabilities" || findings[0].Reach != 4 || len(findings[0].Sources) != 4 {
		t.Fatalf("competitor domains were not grouped into one topic finding: %+v", findings)
	}
}

func linkedSnapshot() MonitoringSnapshot {
	return MonitoringSnapshot{BusinessName: "Customer Clinic", Runs: []SnapshotRun{
		{RunID: "run-1", Results: []SnapshotResult{
			{ResultID: "linked-1", PromptID: "prompt-1", Citations: []SnapshotCitation{{URL: "https://www.rival.example/a", Domain: "www.rival.example", Passage: "Rival Clinic offers microscope-assisted treatment.", Competitors: []string{"Rival Clinic"}}}},
			{ResultID: "unlinked", PromptID: "prompt-2", Citations: []SnapshotCitation{{URL: "https://www.rival.example/unlinked", Domain: "www.rival.example", Passage: "General advice."}}},
		}},
		{RunID: "run-2", Results: []SnapshotResult{
			{ResultID: "linked-2", PromptID: "prompt-1", Citations: []SnapshotCitation{{URL: "https://www.rival.example/b", Domain: "www.rival.example", Passage: "Rival Clinic handles complex cases.", Competitors: []string{"Rival Clinic"}}}},
		}},
	}}
}

func TestCitationGapCountsOnlyLinkedAbsentResults(t *testing.T) {
	classifier := &fixedSourceClassifier{kind: llm.SourceUnknown}
	research := &fixtureResearcher{remaining: ResearchURLBudget, pages: map[string]string{
		"https://www.rival.example/a": "<h1>Rival Clinic</h1>",
		"https://www.rival.example/b": "<h1>Rival Clinic services</h1>",
	}}
	findings, err := citationGapFinder{}.Find(context.Background(), FinderInput{Snapshot: linkedSnapshot(), Classifier: classifier}, research)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Reach != 2 || len(findings[0].ResultIDs) != 2 {
		t.Fatalf("unlinked result inflated finding: %+v", findings)
	}
	if strings.Contains(findings[0].Body, "3 answers") || !strings.Contains(findings[0].Body, "In 2 answers that omitted you") {
		t.Fatalf("body has misleading count: %q", findings[0].Body)
	}
	if len(classifier.in.Candidates) != 1 || len(classifier.in.Candidates[0].Claims) != 2 {
		t.Fatalf("classifier input lost linked claims: %+v", classifier.in)
	}
	if classifier.calls != 1 {
		t.Fatalf("classifier calls = %d, want one batch", classifier.calls)
	}
}

func TestCompetitorOwnedSourceBecomesContentAction(t *testing.T) {
	classifier := &fixedSourceClassifier{kind: llm.SourceCompetitorOwned, owner: "Rival Clinic"}
	research := &fixtureResearcher{remaining: ResearchURLBudget, pages: map[string]string{
		"https://www.rival.example/a": "<html><title>Rival Clinic</title><body>Official site</body></html>",
		"https://www.rival.example/b": "<html><body>Official Rival Clinic content</body></html>",
	}}
	findings, err := citationGapFinder{}.Find(context.Background(), FinderInput{Snapshot: linkedSnapshot(), SiteContent: "Customer Clinic already describes its team.", Classifier: classifier}, research)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings=%+v", findings)
	}
	if classifier.in.SiteContent != "Customer Clinic already describes its team." || classifier.in.BusinessName != "Customer Clinic" {
		t.Fatalf("customer-site context was not supplied to analysis: %+v", classifier.in)
	}
	f := findings[0]
	if f.Key != "competitor-content:services_capabilities" || f.Category != GroupIdentity || strings.Contains(f.Key, "citation-gap") {
		t.Fatalf("competitor-owned source routed incorrectly: %+v", f)
	}
	if strings.Contains(f.Detail, "Rival Clinic offers") || !strings.Contains(f.Detail, "1 competitor-owned source") {
		t.Errorf("detail should summarize grouped evidence without dumping claims: %q", f.Detail)
	}
	if !strings.Contains(strings.Join(f.Steps, " "), "do not copy") || !strings.Contains(strings.Join(f.Steps, " "), "substantiate") {
		t.Errorf("content action lacks safety constraints: %v", f.Steps)
	}
}

func TestThirdPartyAndUnknownRemainListingActions(t *testing.T) {
	for _, kind := range []string{llm.SourceThirdParty, llm.SourceUnknown} {
		t.Run(kind, func(t *testing.T) {
			classifier := &fixedSourceClassifier{kind: kind}
			research := &fixtureResearcher{remaining: ResearchURLBudget, pages: map[string]string{"https://www.rival.example/a": "<h1>Rival Clinic</h1>", "https://www.rival.example/b": "<h1>Rival Clinic</h1>"}}
			findings, err := citationGapFinder{}.Find(context.Background(), FinderInput{Snapshot: linkedSnapshot(), Classifier: classifier}, research)
			if err != nil || len(findings) != 1 || findings[0].Key != "citation-gap:www.rival.example" {
				t.Fatalf("err=%v findings=%+v", err, findings)
			}
		})
	}
}
