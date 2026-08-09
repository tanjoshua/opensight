package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func classificationCandidates() []SourceCandidate {
	return []SourceCandidate{
		{Domain: "rival.example", Pages: []SourcePage{{URL: "https://rival.example/about", Content: "Official Rival Clinic site"}}, Claims: []SourceClaim{{Owner: "Rival Clinic", Passage: "Rival Clinic offers microscope-assisted treatment."}, {Owner: "Other Clinic", Passage: "Other Clinic opens late."}}},
		{Domain: "directory.example", Pages: []SourcePage{{URL: "https://directory.example/list", Content: "Independent directory"}}, Claims: []SourceClaim{{Owner: "Rival Clinic", Passage: "Rival Clinic is listed."}}},
	}
}

func TestValidateSourceClassifications(t *testing.T) {
	candidates := classificationCandidates()
	valid := []SourceClassification{
		{CandidateIndex: 0, Kind: SourceCompetitorOwned, Owner: "Rival Clinic", ClaimIndices: []int{0}},
		{CandidateIndex: 1, Kind: SourceThirdParty, Owner: "", ClaimIndices: []int{}},
	}
	if errs := validateSourceClassifications(valid, candidates); len(errs) != 0 {
		t.Fatalf("valid classifications rejected: %v", errs)
	}

	tests := []struct {
		name string
		out  []SourceClassification
		want string
	}{
		{"unknown is valid", []SourceClassification{{CandidateIndex: 0, Kind: SourceUnknown, ClaimIndices: []int{}}, {CandidateIndex: 1, Kind: SourceUnknown, ClaimIndices: []int{}}}, ""},
		{"missing candidate", valid[:1], "missing"},
		{"duplicate candidate", []SourceClassification{valid[0], valid[0]}, "duplicated"},
		{"invalid candidate index", []SourceClassification{{CandidateIndex: 9, Kind: SourceUnknown}, valid[1]}, "out of range"},
		{"invalid enum", []SourceClassification{{CandidateIndex: 0, Kind: "owned"}, valid[1]}, "invalid classification"},
		{"wrong owner claim", []SourceClassification{{CandidateIndex: 0, Kind: SourceCompetitorOwned, Owner: "Rival Clinic", ClaimIndices: []int{1}}, valid[1]}, "belongs to"},
		{"multiple owners require one selected owner", []SourceClassification{{CandidateIndex: 0, Kind: SourceCompetitorOwned, Owner: "Other Clinic", ClaimIndices: []int{0, 1}}, valid[1]}, "belongs to"},
		{"invalid claim index", []SourceClassification{{CandidateIndex: 0, Kind: SourceCompetitorOwned, Owner: "Rival Clinic", ClaimIndices: []int{7}}, valid[1]}, "claim index"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateSourceClassifications(tc.out, candidates)
			if tc.want == "" && len(errs) != 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if tc.want != "" && !containsSubstr(errs, tc.want) {
				t.Fatalf("errors %v do not contain %q", errs, tc.want)
			}
		})
	}
}

func TestValidateGroupedContentGaps(t *testing.T) {
	in := SourceClassificationInput{
		SiteContent: "Our specialist team has postgraduate training.",
		Candidates:  classificationCandidates(),
	}
	sources := []SourceClassification{
		{CandidateIndex: 0, Kind: SourceCompetitorOwned, Owner: "Rival Clinic", ClaimIndices: []int{0}},
		{CandidateIndex: 1, Kind: SourceThirdParty, ClaimIndices: []int{}},
	}
	valid := ContentGap{
		Topic: ContentTopicTechnology, Title: "Explain the technology used in treatment",
		Reason:         "The site names specialist training but not the treatment technology.",
		Recommendation: "On the services page, state which imaging or magnification tools are used, if applicable.",
		Coverage:       "partial", SiteEvidence: []string{"Our specialist team has postgraduate training."},
		Evidence: []SourceClaimReference{{CandidateIndex: 0, ClaimIndex: 0}},
	}
	if errs := validateSourceAnalysis(SourceAnalysis{Sources: sources, Gaps: []ContentGap{valid}}, in); len(errs) != 0 {
		t.Fatalf("valid grouped gap rejected: %v", errs)
	}

	tests := []struct {
		name string
		gap  ContentGap
		want string
	}{
		{"duplicate topic", valid, "duplicated"},
		{"non-verbatim site evidence", func() ContentGap { g := valid; g.SiteEvidence = []string{"not on site"}; return g }(), "not verbatim"},
		{"third-party evidence", func() ContentGap {
			g := valid
			g.Evidence = []SourceClaimReference{{CandidateIndex: 1, ClaimIndex: 0}}
			return g
		}(), "not selected as competitor-owned"},
		{"competitor named in action", func() ContentGap { g := valid; g.Title = "Match Rival Clinic technology"; return g }(), "names competitor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gaps := []ContentGap{tc.gap}
			if tc.name == "duplicate topic" {
				gaps = append(gaps, valid)
			}
			errs := validateSourceAnalysis(SourceAnalysis{Sources: sources, Gaps: gaps}, in)
			if !containsSubstr(errs, tc.want) {
				t.Fatalf("errors %v do not contain %q", errs, tc.want)
			}
		})
	}
	missingSite := in
	missingSite.SiteContent = ""
	if errs := validateSourceAnalysis(SourceAnalysis{Sources: sources, Gaps: []ContentGap{valid}}, missingSite); !containsSubstr(errs, "unavailable") {
		t.Fatalf("unreadable customer site accepted a content gap: %v", errs)
	}
}

type classificationSequence struct {
	outputs []json.RawMessage
	calls   int
	inputs  []SourceClassificationInput
}

func (s *classificationSequence) runSourceClassification(_ context.Context, in SourceClassificationInput) (SourceClassificationRunResult, error) {
	s.inputs = append(s.inputs, in)
	out := s.outputs[s.calls]
	s.calls++
	return SourceClassificationRunResult{RawJSON: out, Model: "fake-mini"}, nil
}

func TestClassifySourcesRetriesValidationOnce(t *testing.T) {
	caller := &classificationSequence{outputs: []json.RawMessage{
		json.RawMessage(`{"sources":[{"candidate_index":0,"classification":"competitor_owned","owner":"Rival Clinic","claim_indices":[1]},{"candidate_index":1,"classification":"third_party","owner":"","claim_indices":[]}],"content_gaps":[]}`),
		json.RawMessage(`{"sources":[{"candidate_index":0,"classification":"competitor_owned","owner":"Rival Clinic","claim_indices":[0]},{"candidate_index":1,"classification":"third_party","owner":"","claim_indices":[]}],"content_gaps":[]}`),
	}}
	got, err := classifySourcesWithRetry(context.Background(), caller, SourceClassificationInput{Candidates: classificationCandidates()})
	if err != nil {
		t.Fatal(err)
	}
	if caller.calls != 2 || len(got.Sources) != 2 {
		t.Fatalf("calls=%d output=%v", caller.calls, got)
	}
	if len(caller.inputs[1].RetryValidationErrors) == 0 || !strings.Contains(string(caller.inputs[1].PriorOutputJSON), `"claim_indices":[1]`) {
		t.Fatalf("retry did not carry prior output and validation errors: %+v", caller.inputs[1])
	}
}

func TestClassifySourcesFailsAfterRetry(t *testing.T) {
	bad := json.RawMessage(`{"sources":[],"content_gaps":[]}`)
	caller := &classificationSequence{outputs: []json.RawMessage{bad, bad}}
	_, err := classifySourcesWithRetry(context.Background(), caller, SourceClassificationInput{Candidates: classificationCandidates()})
	if err == nil || caller.calls != 2 {
		t.Fatalf("err=%v calls=%d, want validation failure after two calls", err, caller.calls)
	}
}
