package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// validProposal is a well-formed 4-prompt proposal (one prompt of each kind,
// none naming the business) used as the baseline the negative cases mutate.
func validProposal() ProposalPayload {
	return ProposalPayload{
		LowConfidence: false,
		Profile: ProposedProfile{
			Name:          "Novena Orthopaedic Clinic",
			Aliases:       []string{"Novena Ortho"},
			Category:      "orthopaedic clinic",
			Practitioners: []ProposedPractitioner{{Name: "Dr Tan", Role: ""}},
			Services:      []string{"ACL reconstruction"},
			Location:      ProposedLocation{City: "Singapore", Country: "SG"},
		},
		Prompts: []ProposedPrompt{
			{Text: "best orthopaedic clinic in Singapore", Kind: "category"},
			{Text: "where can I get ACL reconstruction in Singapore", Kind: "service"},
			{Text: "knee pain that won't go away, who should I see", Kind: "condition"},
			{Text: "orthopaedic specialist near Novena MRT", Kind: "location"},
		},
	}
}

func TestValidateProposal(t *testing.T) {
	in := ProposeProfileInput{Name: "Novena Orthopaedic Clinic", PromptLimit: 4}

	tests := []struct {
		name     string
		mutate   func(*ProposalPayload)
		wantErr  string // substring the error list must contain; "" means expect no errors
		wantNone bool
	}{
		{name: "valid", mutate: func(p *ProposalPayload) {}, wantNone: true},
		{name: "wrong prompt count", mutate: func(p *ProposalPayload) { p.Prompts = p.Prompts[:3] }, wantErr: "want exactly 4"},
		{name: "empty name", mutate: func(p *ProposalPayload) { p.Profile.Name = "  " }, wantErr: "profile.name is empty"},
		{name: "empty category", mutate: func(p *ProposalPayload) { p.Profile.Category = "" }, wantErr: "profile.category is empty"},
		{name: "missing country", mutate: func(p *ProposalPayload) { p.Profile.Location.Country = "" }, wantErr: "country is empty"},
		{name: "malformed country", mutate: func(p *ProposalPayload) { p.Profile.Location.Country = "Singapore" }, wantErr: "two-letter ISO"},
		{name: "empty service entry", mutate: func(p *ProposalPayload) { p.Profile.Services = []string{""} }, wantErr: "services[0] is empty"},
		{name: "empty prompt text", mutate: func(p *ProposalPayload) { p.Prompts[0].Text = " " }, wantErr: "prompts[0].text is empty"},
		{name: "unknown kind", mutate: func(p *ProposalPayload) { p.Prompts[0].Kind = "branded" }, wantErr: `"branded" is not one of`},
		{
			name:    "alias leaks into prompt (case-insensitive)",
			mutate:  func(p *ProposalPayload) { p.Prompts[0].Text = "is NOVENA ORTHO any good" },
			wantErr: "contains the business name/alias",
		},
		{
			// Leakage is checked against the input Name too, not only profile.Name.
			name:    "input name leaks into prompt",
			mutate:  func(p *ProposalPayload) { p.Prompts[2].Text = "reviews of novena orthopaedic clinic" },
			wantErr: "contains the business name/alias",
		},
		{name: "all one kind", mutate: func(p *ProposalPayload) {
			for i := range p.Prompts {
				p.Prompts[i].Kind = "category"
			}
		}, wantErr: "distinct kinds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validProposal()
			tt.mutate(&p)
			errs := ValidateProposal(p, in)
			if tt.wantNone {
				if len(errs) != 0 {
					t.Fatalf("want no errors, got %v", errs)
				}
				return
			}
			if !containsSubstr(errs, tt.wantErr) {
				t.Fatalf("want an error containing %q, got %v", tt.wantErr, errs)
			}
		})
	}
}

// TestValidateProposalShortNameNoFalsePositive guards the minNameLeakLength
// rule: a one-letter business name must not flag ordinary words in prompts.
func TestValidateProposalShortNameNoFalsePositive(t *testing.T) {
	in := ProposeProfileInput{Name: "Q", PromptLimit: 4}
	p := validProposal()
	p.Profile.Name = "Q"
	p.Profile.Aliases = nil
	errs := ValidateProposal(p, in)
	for _, e := range errs {
		if strings.Contains(e, "business name/alias") {
			t.Fatalf("short name should not trigger leakage, got %v", errs)
		}
	}
}

// fakeProposeRunner returns a queued sequence of results, recording the input of
// each call so the retry path (prior output + validation errors) can be asserted.
type fakeProposeRunner struct {
	results []ProposeProfileRunResult
	calls   []ProposeProfileInput
}

func (r *fakeProposeRunner) RunProposeProfile(_ context.Context, in ProposeProfileInput) (ProposeProfileRunResult, error) {
	r.calls = append(r.calls, in)
	res := r.results[len(r.calls)-1]
	return res, nil
}

func mustMarshal(t *testing.T, p ProposalPayload) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestProposeWithRetryInvalidThenValid(t *testing.T) {
	in := ProposeProfileInput{Name: "Novena Orthopaedic Clinic", PromptLimit: 4}

	invalid := validProposal()
	invalid.Prompts = invalid.Prompts[:2] // wrong count
	valid := validProposal()

	runner := &fakeProposeRunner{results: []ProposeProfileRunResult{
		{RawJSON: mustMarshal(t, invalid), Model: "m1"},
		{RawJSON: mustMarshal(t, valid), Model: "m2"},
	}}

	res, err := ProposeWithRetry(context.Background(), runner, in)
	if err != nil {
		t.Fatalf("ProposeWithRetry: %v", err)
	}
	if !res.Proposed {
		t.Fatalf("want Proposed after valid retry, got %+v", res)
	}
	if res.Model != "m2" {
		t.Fatalf("Model = %q, want m2", res.Model)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner called %d times, want 2", len(runner.calls))
	}
	// The second call must carry the prior output and the validation errors.
	second := runner.calls[1]
	if len(second.PriorOutputJSON) == 0 {
		t.Fatal("retry call missing prior output JSON")
	}
	if len(second.RetryValidationErrors) == 0 {
		t.Fatal("retry call missing validation errors")
	}
}

func TestProposeWithRetryAlwaysInvalid(t *testing.T) {
	in := ProposeProfileInput{Name: "Novena Orthopaedic Clinic", PromptLimit: 4}
	invalid := validProposal()
	invalid.Profile.Category = "" // always fails

	runner := &fakeProposeRunner{results: []ProposeProfileRunResult{
		{RawJSON: mustMarshal(t, invalid), Model: "m1"},
		{RawJSON: mustMarshal(t, invalid), Model: "m2"},
	}}

	res, err := ProposeWithRetry(context.Background(), runner, in)
	if err != nil {
		t.Fatalf("ProposeWithRetry: %v", err)
	}
	if res.Proposed {
		t.Fatal("want Proposed=false after exhausting attempts")
	}
	if len(res.ValidationErrs) == 0 {
		t.Fatal("want validation errors populated when not proposed")
	}
	if len(runner.calls) != MaxProposeProfileAttempts {
		t.Fatalf("runner called %d times, want %d", len(runner.calls), MaxProposeProfileAttempts)
	}
}
