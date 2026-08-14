package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func classificationCandidates() []SourceCandidate {
	return []SourceCandidate{
		{Domain: "rival.example", Pages: []SourcePage{{URL: "https://rival.example/about", Content: "Official Rival Clinic site"}}, Claims: []SourceClaim{{Owner: "Rival Clinic", Passage: "Rival Clinic offers microscope-assisted treatment.", Question: "Which clinic handles complex cases?"}, {Owner: "Other Clinic", Passage: "Other Clinic opens late.", Question: "Which clinic opens late?"}}},
		{Domain: "directory.example", Pages: []SourcePage{{URL: "https://directory.example/list", Content: "Independent directory"}}, Claims: []SourceClaim{{Owner: "Rival Clinic", Passage: "Rival Clinic is listed.", Question: "Which clinic handles complex cases?"}}},
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

// A bad opportunity costs only that opportunity: ownership classification and
// other grounded opportunities still survive.
func TestUnusableContentOpportunityIsDroppedNotFatal(t *testing.T) {
	in := SourceClassificationInput{
		SiteContent: "Our specialist team has postgraduate training.",
		Candidates:  classificationCandidates(),
	}
	caller := &classificationSequence{outputs: []json.RawMessage{json.RawMessage(`{
		"sources":[
			{"candidate_index":0,"classification":"competitor_owned","owner":"Rival Clinic","claim_indices":[0]},
			{"candidate_index":1,"classification":"third_party","owner":"","claim_indices":[]}
		],
		"content_opportunities":[
			{"topic_key":"treatment-technology","title":"Clarify treatment technology","observation":"A monitored answer highlighted microscope-assisted treatment.","site_state":"The pages we read describe training without naming equipment.","suggested_action":"If applicable, state which magnification tools are used.","coverage":"partial","site_evidence":["Our  specialist team has postgraduate training"],"evidence":[{"candidate_index":0,"claim_index":0,"supported_point":"microscope-assisted treatment"}]},
			{"topic_key":"directory-presence","title":"Get listed","observation":"A directory was cited.","site_state":"The pages we read do not discuss it.","suggested_action":"Add a listing.","coverage":"absent","site_evidence":[],"evidence":[{"candidate_index":1,"claim_index":0,"supported_point":"directory listing"}]}
		]}`)}}
	got, err := classifySourcesWithRetry(context.Background(), caller, in)
	if err != nil || caller.calls != 1 {
		t.Fatalf("err=%v calls=%d, want one call and no error", err, caller.calls)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("sources = %v, want both classifications", got.Sources)
	}
	if len(got.Opportunities) != 1 || got.Opportunities[0].Key != "treatment-technology" {
		t.Fatalf("opportunities = %v, want only the competitor-owned opportunity", got.Opportunities)
	}

	// Absence cannot be verified without readable customer-site content.
	if opportunities := usableContentOpportunities(got.Opportunities, got.Sources, SourceClassificationInput{Candidates: in.Candidates}); len(opportunities) != 0 {
		t.Fatalf("opportunities = %v, want none when the customer site is unreadable", opportunities)
	}
}

func TestContentOpportunityGroundingValidation(t *testing.T) {
	base := ContentOpportunity{
		Key: "treatment-technology", Title: "Clarify treatment technology",
		Observation:     "A monitored answer highlighted microscope-assisted treatment.",
		SiteState:       "The pages we read describe training without naming equipment.",
		SuggestedAction: "If applicable, state which tools are used.", Coverage: "partial",
		SiteEvidence: []string{"Our specialist team has postgraduate training."},
		Evidence:     []SourceClaimReference{{CandidateIndex: 0, ClaimIndex: 0, SupportedPoint: "microscope-assisted treatment"}},
	}
	in := SourceClassificationInput{SiteContent: "Our specialist team has postgraduate training.", Candidates: classificationCandidates()}
	sources := []SourceClassification{{CandidateIndex: 0, Kind: SourceCompetitorOwned}, {CandidateIndex: 1, Kind: SourceThirdParty}}
	if got := usableContentOpportunities([]ContentOpportunity{base}, sources, in); len(got) != 1 {
		t.Fatalf("grounded opportunity rejected: %v", got)
	}
	for name, mutate := range map[string]func(*ContentOpportunity){
		"invalid coverage":         func(o *ContentOpportunity) { o.Coverage = "complete" },
		"partial without evidence": func(o *ContentOpportunity) { o.SiteEvidence = nil },
		"invented site evidence":   func(o *ContentOpportunity) { o.SiteEvidence = []string{"Invented passage"} },
		"missing supported point":  func(o *ContentOpportunity) { o.Evidence[0].SupportedPoint = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.SiteEvidence = append([]string(nil), base.SiteEvidence...)
			candidate.Evidence = append([]SourceClaimReference(nil), base.Evidence...)
			mutate(&candidate)
			if got := usableContentOpportunities([]ContentOpportunity{candidate}, sources, in); len(got) != 0 {
				t.Fatalf("invalid opportunity survived: %+v", got)
			}
		})
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
		json.RawMessage(`{"sources":[{"candidate_index":0,"classification":"competitor_owned","owner":"Rival Clinic","claim_indices":[0]}],"content_opportunities":[]}`),
		json.RawMessage(`{"sources":[{"candidate_index":0,"classification":"competitor_owned","owner":"Rival Clinic","claim_indices":[0]},{"candidate_index":1,"classification":"third_party","owner":"","claim_indices":[]}],"content_opportunities":[]}`),
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
	bad := json.RawMessage(`{"sources":[{"candidate_index":0,"classification":"owned"},{"candidate_index":1,"classification":"third_party"}],"content_opportunities":[]}`)
	caller := &classificationSequence{outputs: []json.RawMessage{bad, bad}}
	_, err := classifySourcesWithRetry(context.Background(), caller, SourceClassificationInput{Candidates: classificationCandidates()})
	if err == nil || caller.calls != 2 {
		t.Fatalf("err=%v calls=%d, want validation failure after two calls", err, caller.calls)
	}
}
