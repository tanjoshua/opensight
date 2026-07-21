package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// ExtractionInput is one AnalyzeResult extraction request: the response to
// analyze plus the target-business context the model needs to judge is_target
// and citation subjects (design 05, "Phase 1 — AnalyzeResult").
type ExtractionInput struct {
	ResponseText     string
	Prompt           string
	BusinessName     string
	BusinessAliases  []string
	BusinessCategory string
	BusinessLocation string // "City, Country" summary
	Citations        []CitationAnnotation

	// Set only on the one allowed validation retry: the model's prior output and
	// the deterministic validation failures to correct.
	PriorOutputJSON       json.RawMessage
	RetryValidationErrors []string
}

// ExtractionRunResult is the raw output of one extraction call. The model's JSON
// text is left unmarshalled for the workflow activity to decode and validate —
// the same split as PromptRunResult vs ExecutePrompt.
type ExtractionRunResult struct {
	RawJSON json.RawMessage // the model's structured-output JSON text
	Model   string          // API-reported model id -> result_analyses.analysis_model
}

// ExtractedEntity is one organization the response recommended or discussed, in
// order of first appearance. is_target is the model's hint; reconcile (ANA-4)
// verifies it against normalized aliases before trusting it.
type ExtractedEntity struct {
	VerbatimName string `json:"verbatim_name"`
	IsTarget     bool   `json:"is_target"`
	Excerpt      string `json:"excerpt"`
}

// ExtractionTarget is how the response characterizes the target business. It is
// nil when the business is not mentioned.
type ExtractionTarget struct {
	Sentiment string   `json:"sentiment"`
	Keywords  []string `json:"keywords"`
	Excerpts  []string `json:"excerpts"`
}

// ExtractedCitation aligns one response citation to a best-effort subject
// judged from the surrounding text only (design 05).
type ExtractedCitation struct {
	URL     string `json:"url"`
	Subject string `json:"subject"`
}

// ExtractionOutput is the decoded extraction schema (design 05). Validation
// (ValidateExtraction) runs over it before any of it is trusted enough to write.
type ExtractionOutput struct {
	Entities  []ExtractedEntity   `json:"entities"`
	Target    *ExtractionTarget   `json:"target"`
	Citations []ExtractedCitation `json:"citations"`
}

// ExtractionRunner runs one structured-output extraction call per succeeded
// prompt_result (design 05). Mirrors PromptRunner's mode split.
type ExtractionRunner interface {
	RunExtraction(ctx context.Context, in ExtractionInput) (ExtractionRunResult, error)
}

// NewExtractionRunner selects an ExtractionRunner by mode, mirroring
// NewPromptRunner. "stub" and "replay" spend no OpenAI money; "openai" is the
// real call. "replay" is aliased to the stub for now — building a real
// replay-fixture corpus for extraction is ANA-3's job. openAICfg is only
// consulted for "openai".
func NewExtractionRunner(mode string, openAICfg OpenAIConfig) (ExtractionRunner, error) {
	switch mode {
	case "stub", "replay":
		return NewStubExtractionRunner()
	case "openai":
		return NewOpenAIExtractionRunner(openAICfg)
	default:
		return nil, fmt.Errorf("unknown extraction runner mode %q", mode)
	}
}

// StubExtractionRunner always returns an empty, trivially-valid extraction (no
// entities, no target, no citations) regardless of input, so the dev default
// (PROMPT_RUNNER_MODE=stub) boots the worker without an OpenAI key.
type StubExtractionRunner struct{}

// NewStubExtractionRunner returns the offline stub extractor.
func NewStubExtractionRunner() (*StubExtractionRunner, error) {
	return &StubExtractionRunner{}, nil
}

// RunExtraction returns the canned empty extraction. The model id is a fixed
// stub marker so a stubbed row is distinguishable from a real one.
func (r *StubExtractionRunner) RunExtraction(_ context.Context, _ ExtractionInput) (ExtractionRunResult, error) {
	return ExtractionRunResult{
		RawJSON: json.RawMessage(`{"entities":[],"target":null,"citations":[]}`),
		Model:   "stub-extraction",
	}, nil
}
