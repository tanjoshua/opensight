package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// validQuestionsInput and validQuestions are a matched 4-prompt baseline (none
// naming the business) that the negative cases mutate.
func validQuestionsInput() QuestionsInput {
	return QuestionsInput{
		Name:        "Novena Orthopaedic Clinic",
		Aliases:     []string{"Novena Ortho"},
		Category:    "orthopaedic clinic",
		Services:    []string{"ACL reconstruction"},
		City:        "Singapore",
		PromptLimit: 4,
	}
}

func validQuestions() []ProposedPrompt {
	return []ProposedPrompt{
		{Text: "best orthopaedic clinic in Singapore"},
		{Text: "which orthopaedic clinics in Singapore offer ACL reconstruction"},
		{Text: "which specialist in Singapore treats knee pain that won't go away"},
		{Text: "where can I get an orthopaedic second opinion in Singapore"},
	}
}

func TestValidateQuestions(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(in *QuestionsInput, prompts *[]ProposedPrompt)
		wantErr  string
		wantNone bool
	}{
		{name: "valid", mutate: func(*QuestionsInput, *[]ProposedPrompt) {}, wantNone: true},
		{
			name:    "wrong prompt count",
			mutate:  func(_ *QuestionsInput, prompts *[]ProposedPrompt) { *prompts = (*prompts)[:3] },
			wantErr: "want exactly 4",
		},
		{
			name:    "empty prompt text",
			mutate:  func(_ *QuestionsInput, prompts *[]ProposedPrompt) { (*prompts)[0].Text = " " },
			wantErr: "prompts[0].text is empty",
		},
		{
			name:    "alias leaks into prompt (case-insensitive)",
			mutate:  func(_ *QuestionsInput, prompts *[]ProposedPrompt) { (*prompts)[0].Text = "is NOVENA ORTHO any good" },
			wantErr: "contains the business name/alias",
		},
		{
			name: "name leaks into prompt",
			mutate: func(_ *QuestionsInput, prompts *[]ProposedPrompt) {
				(*prompts)[2].Text = "reviews of novena orthopaedic clinic"
			},
			wantErr: "contains the business name/alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validQuestionsInput()
			prompts := validQuestions()
			tt.mutate(&in, &prompts)
			errs := ValidateQuestions(prompts, in)
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

// TestValidateQuestionsShortNameNoFalsePositive guards the minNameLeakLength
// rule: a one-letter business name must not flag ordinary words in questions.
func TestValidateQuestionsShortNameNoFalsePositive(t *testing.T) {
	in := validQuestionsInput()
	in.Name = "Q"
	in.Aliases = nil
	errs := ValidateQuestions(validQuestions(), in)
	for _, e := range errs {
		if strings.Contains(e, "business name/alias") {
			t.Fatalf("short name should not trigger leakage, got %v", errs)
		}
	}
}

// fakeQuestionsRunner returns a queued sequence of results, recording the
// input of each call so the retry path (prior output + validation errors) can
// be asserted.
type fakeQuestionsRunner struct {
	results []QuestionsRunResult
	calls   []QuestionsInput
}

func (r *fakeQuestionsRunner) RunQuestions(_ context.Context, in QuestionsInput) (QuestionsRunResult, error) {
	r.calls = append(r.calls, in)
	res := r.results[len(r.calls)-1]
	return res, nil
}

func mustMarshalQuestions(t *testing.T, prompts []ProposedPrompt) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(questionsOutput{Prompts: prompts})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestGenerateQuestionsWithRetryInvalidThenValid(t *testing.T) {
	in := validQuestionsInput()

	invalid := validQuestions()[:2] // wrong count
	valid := validQuestions()

	runner := &fakeQuestionsRunner{results: []QuestionsRunResult{
		{RawJSON: mustMarshalQuestions(t, invalid), Model: "m1"},
		{RawJSON: mustMarshalQuestions(t, valid), Model: "m2"},
	}}

	res, err := GenerateQuestionsWithRetry(context.Background(), runner, in)
	if err != nil {
		t.Fatalf("GenerateQuestionsWithRetry: %v", err)
	}
	if !res.Generated {
		t.Fatalf("want Generated after valid retry, got %+v", res)
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

func TestGenerateQuestionsWithRetryAlwaysInvalid(t *testing.T) {
	in := validQuestionsInput()
	invalid := validQuestions()[:2] // always wrong count

	runner := &fakeQuestionsRunner{results: []QuestionsRunResult{
		{RawJSON: mustMarshalQuestions(t, invalid), Model: "m1"},
		{RawJSON: mustMarshalQuestions(t, invalid), Model: "m2"},
	}}

	res, err := GenerateQuestionsWithRetry(context.Background(), runner, in)
	if err != nil {
		t.Fatalf("GenerateQuestionsWithRetry: %v", err)
	}
	if res.Generated {
		t.Fatal("want Generated=false after exhausting attempts")
	}
	if len(res.ValidationErrs) == 0 {
		t.Fatal("want validation errors populated when not generated")
	}
	if len(runner.calls) != MaxQuestionsAttempts {
		t.Fatalf("runner called %d times, want %d", len(runner.calls), MaxQuestionsAttempts)
	}
}
