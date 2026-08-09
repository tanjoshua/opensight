package visibility

import (
	"context"
	"strings"
	"testing"

	"opensight/internal/llm"
)

type fixedSourceClassifier struct {
	kind  string
	owner string
	calls int
	in    llm.SourceClassificationInput
}

func (c *fixedSourceClassifier) ClassifySources(_ context.Context, in llm.SourceClassificationInput) ([]llm.SourceClassification, error) {
	c.calls++
	c.in = in
	out := make([]llm.SourceClassification, len(in.Candidates))
	for i, candidate := range in.Candidates {
		out[i] = llm.SourceClassification{CandidateIndex: i, Kind: c.kind, ClaimIndices: []int{}}
		if c.kind == llm.SourceCompetitorOwned {
			out[i].Owner = c.owner
			for j, claim := range candidate.Claims {
				if claim.Owner == c.owner {
					out[i].ClaimIndices = append(out[i].ClaimIndices, j)
				}
			}
		}
	}
	return out, nil
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
	findings, err := citationGapFinder{}.Find(context.Background(), FinderInput{Snapshot: linkedSnapshot(), Classifier: classifier}, research)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings=%+v", findings)
	}
	f := findings[0]
	if f.Key != "competitor-content:www.rival.example" || f.Category != GroupIdentity || strings.Contains(f.Key, "citation-gap") {
		t.Fatalf("competitor-owned source routed incorrectly: %+v", f)
	}
	for _, claim := range []string{"Rival Clinic offers microscope-assisted treatment.", "Rival Clinic handles complex cases."} {
		if !strings.Contains(f.Detail, claim) {
			t.Errorf("detail lost exact claim %q: %q", claim, f.Detail)
		}
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
