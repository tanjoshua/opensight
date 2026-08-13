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
// text is left unmarshalled for the analysis operation to decode and validate —
// the same split as PromptRunResult vs ExecutePrompt.
type ExtractionRunResult struct {
	RawJSON json.RawMessage // the model's structured-output JSON text
	Model   string          // API-reported model id -> result_analyses.analysis_model
}

// ExtractedEntity is one organization the response recommended or discussed, in
// order of first appearance. is_target is the model's hint; reconcile
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
	CiteOrder int                  `json:"cite_order"`
	URL       string               `json:"url"`
	Subject   string               `json:"subject"`
	Links     []CitationEntityLink `json:"links"`
}

// CitationEntityLink carries enough response-local evidence to validate that
// an entity index was not shifted onto the wrong organization. Reference is the
// exact name or shorthand used in the response; Passage is the exact claim
// immediately supported by this citation occurrence.
type CitationEntityLink struct {
	EntityIndex int    `json:"entity_index"`
	Reference   string `json:"reference"`
	Passage     string `json:"passage"`
}

// ExtractionOutput is the decoded extraction schema (design 05). Validation
// (ValidateExtraction) runs over it before any of it is trusted enough to write.
type ExtractionOutput struct {
	Entities  []ExtractedEntity   `json:"entities"`
	Target    *ExtractionTarget   `json:"target"`
	Citations []ExtractedCitation `json:"citations"`
}

// EntityWithCitations carries an extracted entity into reconciliation together
// with every citation occurrence the model directly linked to it.
type EntityWithCitations struct {
	Entity     ExtractedEntity
	CiteOrders []int
}

// LinkEntities inverts citations[].links into the per-entity shape
// reconciliation needs. ValidateExtraction has already guaranteed the indexes.
func LinkEntities(out ExtractionOutput) []EntityWithCitations {
	linked := make([]EntityWithCitations, len(out.Entities))
	for i, entity := range out.Entities {
		linked[i] = EntityWithCitations{Entity: entity, CiteOrders: []int{}}
	}
	for _, citation := range out.Citations {
		for _, link := range citation.Links {
			linked[link.EntityIndex].CiteOrders = append(linked[link.EntityIndex].CiteOrders, citation.CiteOrder)
		}
	}
	return linked
}

// ExtractionRunner runs one structured-output extraction call per succeeded
// prompt_result (design 05).
type ExtractionRunner interface {
	RunExtraction(ctx context.Context, in ExtractionInput) (ExtractionRunResult, error)
}

// NewExtractionRunner selects an ExtractionRunner by mode (see the package
// doc). openAICfg is only consulted for "openai".
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
