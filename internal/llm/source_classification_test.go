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
		{"missing candidate", valid[:1], "missing"},
		{"duplicate candidate", []SourceClassification{valid[0], valid[0]}, "duplicated"},
		{"invalid candidate index", []SourceClassification{{CandidateIndex: 9, Kind: SourceUnknown}, valid[1]}, "out of range"},
		{"invalid enum", []SourceClassification{{CandidateIndex: 0, Kind: "owned"}, valid[1]}, "invalid classification"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if errs := validateSourceClassifications(tc.out, candidates); !containsSubstr(errs, tc.want) {
				t.Fatalf("errors %v do not contain %q", errs, tc.want)
			}
		})
	}
}

// A gap the model got wrong must cost that gap alone: the run still returns the
// usable gap and every source classification the rest of Improve depends on.
// The kept gap also carries loosely-quoted site_evidence, which is grounding
// only and must never decide whether the gap survives.
func TestUnusableContentGapIsDroppedNotFatal(t *testing.T) {
	in := SourceClassificationInput{
		SiteContent: "Our specialist team has postgraduate training.",
		Candidates:  classificationCandidates(),
	}
	caller := &classificationSequence{outputs: []json.RawMessage{json.RawMessage(`{
		"sources":[
			{"candidate_index":0,"classification":"competitor_owned","owner":"Rival Clinic","claim_indices":[0]},
			{"candidate_index":1,"classification":"third_party","owner":"","claim_indices":[]}
		],
		"content_gaps":[
			{"topic_key":"treatment-technology","title":"Explain the technology used in treatment","reason":"The site names training but not the technology.","recommendation":"State which magnification tools are used, if applicable.","coverage":"partial","site_evidence":["Our  specialist team has postgraduate training"],"evidence":[{"candidate_index":0,"claim_index":0}]},
			{"topic_key":"directory-presence","title":"Get listed","reason":"Cited by a directory.","recommendation":"Add a listing.","coverage":"absent","site_evidence":[],"evidence":[{"candidate_index":1,"claim_index":0}]}
		]}`)}}
	got, err := classifySourcesWithRetry(context.Background(), caller, in)
	if err != nil || caller.calls != 1 {
		t.Fatalf("err=%v calls=%d, want one call and no error", err, caller.calls)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("sources = %v, want both classifications", got.Sources)
	}
	if len(got.Gaps) != 1 || got.Gaps[0].Key != "treatment-technology" {
		t.Fatalf("gaps = %v, want only the competitor-owned gap", got.Gaps)
	}

	// Absence cannot be verified without readable customer-site content.
	if gaps := usableContentGaps(got.Gaps, got.Sources, SourceClassificationInput{Candidates: in.Candidates}); len(gaps) != 0 {
		t.Fatalf("gaps = %v, want none when the customer site is unreadable", gaps)
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
		json.RawMessage(`{"sources":[{"candidate_index":0,"classification":"competitor_owned","owner":"Rival Clinic","claim_indices":[0]}],"content_gaps":[]}`),
		json.RawMessage(`{"sources":[{"candidate_index":0,"classification":"competitor_owned","owner":"Rival Clinic","claim_indices":[0]},{"candidate_index":1,"classification":"third_party","owner":"","claim_indices":[]}],"content_gaps":[]}`),
	}}
	got, err := classifySourcesWithRetry(context.Background(), caller, SourceClassificationInput{Candidates: classificationCandidates()})
	if err != nil {
		t.Fatal(err)
	}
	if caller.calls != 2 || len(got.Sources) != 2 {
		t.Fatalf("calls=%d output=%v", caller.calls, got)
	}
	if len(caller.inputs[1].RetryValidationErrors) == 0 || !strings.Contains(string(caller.inputs[1].PriorOutputJSON), `"sources":[{"candidate_index":0`) {
		t.Fatalf("retry did not carry prior output and validation errors: %+v", caller.inputs[1])
	}
}

func TestClassifySourcesFailsAfterRetry(t *testing.T) {
	bad := json.RawMessage(`{"sources":[{"candidate_index":0,"classification":"owned"},{"candidate_index":1,"classification":"third_party"}],"content_gaps":[]}`)
	caller := &classificationSequence{outputs: []json.RawMessage{bad, bad}}
	_, err := classifySourcesWithRetry(context.Background(), caller, SourceClassificationInput{Candidates: classificationCandidates()})
	if err == nil || caller.calls != 2 {
		t.Fatalf("err=%v calls=%d, want validation failure after two calls", err, caller.calls)
	}
}
