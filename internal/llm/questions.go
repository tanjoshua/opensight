package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// QuestionsInput is one on-demand customer-question generation request (design
// 03): fired once the user has confirmed the business profile through the
// Services review step, so — unlike ProposeProfile — this call does no
// research of its own. Name and Aliases are needed only for the business-name
// leakage check (a question must never name the business), not as generation
// content beyond Category/Services/City. PromptLimit is
// billing.Plan.PromptLimit, resolved by the caller — never hardcoded.
type QuestionsInput struct {
	Name        string
	Aliases     []string
	Category    string
	Services    []string
	City        string
	PromptLimit int

	// Set only on the one allowed validation retry: the model's prior output and
	// the deterministic validation failures to correct.
	PriorOutputJSON       json.RawMessage
	RetryValidationErrors []string
}

// QuestionsRunResult is the raw output of one questions call, left
// unmarshalled for GenerateQuestionsWithRetry to decode and validate — the
// same runner/decoder split as extraction, match, and propose-profile.
type QuestionsRunResult struct {
	RawJSON json.RawMessage
	Model   string
}

// questionsOutput is the decoded schema: one prompt list, no other fields.
type questionsOutput struct {
	Prompts []ProposedPrompt `json:"prompts"`
}

// QuestionsRunner runs one customer-question generation call (design 03,
// "Prompt generation rules" — fired on demand after Services review rather
// than alongside profile research).
type QuestionsRunner interface {
	RunQuestions(ctx context.Context, in QuestionsInput) (QuestionsRunResult, error)
}

// NewQuestionsRunner selects a QuestionsRunner by mode (see the package doc).
// openAICfg is only consulted for "openai".
func NewQuestionsRunner(mode string, openAICfg OpenAIConfig) (QuestionsRunner, error) {
	switch mode {
	case "stub", "replay":
		return NewStubQuestionsRunner()
	case "openai":
		return NewOpenAIQuestionsRunner(openAICfg)
	default:
		return nil, fmt.Errorf("unknown questions runner mode %q", mode)
	}
}

// StubQuestionsRunner returns exactly PromptLimit generic, name-free questions,
// so the dev default and handler tests need no OpenAI key.
type StubQuestionsRunner struct{}

// NewStubQuestionsRunner returns the offline stub question generator.
func NewStubQuestionsRunner() (*StubQuestionsRunner, error) {
	return &StubQuestionsRunner{}, nil
}

// stubQuestionsPrompts are varied, name-free consumer questions the stub
// cycles through to satisfy validation offline.
var stubQuestionsPrompts = []ProposedPrompt{
	{Text: "best orthopaedic clinic in Singapore"},
	{Text: "where can I get ACL reconstruction in Singapore"},
	{Text: "knee pain that won't go away, who should I see in Singapore"},
	{Text: "top rated orthopaedic specialist in Singapore"},
}

// RunQuestions returns a canned prompt list shaped by in.PromptLimit. The
// model id is a fixed stub marker so a stubbed result is distinguishable from
// a real one.
func (r *StubQuestionsRunner) RunQuestions(_ context.Context, in QuestionsInput) (QuestionsRunResult, error) {
	if r == nil {
		return QuestionsRunResult{}, fmt.Errorf("stub questions runner is nil")
	}
	prompts := make([]ProposedPrompt, 0, in.PromptLimit)
	for i := 0; i < in.PromptLimit; i++ {
		prompts = append(prompts, stubQuestionsPrompts[i%len(stubQuestionsPrompts)])
	}
	raw, err := json.Marshal(questionsOutput{Prompts: prompts})
	if err != nil {
		return QuestionsRunResult{}, err
	}
	return QuestionsRunResult{RawJSON: raw, Model: "stub-questions"}, nil
}
