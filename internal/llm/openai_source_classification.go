package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const openAISourceClassificationSchemaName = "citation_source_classification"

const sourceClassificationInstructions = `Classify each inspected citation source using only the supplied domain, page identity/content, linked competitor names, and exact cited claims.

CLASSIFICATIONS
- competitor_owned: the domain/page is the official site or first-party content of exactly one linked competitor. A same-domain page title or visible brand name that clearly matches that competitor is sufficient ownership evidence, including when the page is a JavaScript shell with little other visible text.
- third_party: the source independently lists, reviews, reports on, or otherwise covers businesses.
- unknown: ownership cannot be established confidently from the supplied evidence.

RULES
- Emit exactly one source for every candidate_index, without adding, dropping, or duplicating indexes.
- For competitor_owned, owner must exactly copy one linked claim owner and claim_indices must select only claims belonging to that owner. Select the exact claims that would help the customer understand what comparable truthful evidence to publish on its own site.
- For third_party and unknown, return owner "" and claim_indices [].
- Prefer the competitor whose name matches the page title/visible brand when several linked names occur in claims. Do not assign the page to the other businesses merely because the same cited passage names them too.
- Do not infer ownership from a domain name alone when neither the page identity nor inspected content confirms it.
- If prior output and validation failures are supplied, fix every failure and emit the full corrected object.`

const sourceClassificationJSONSchema = `{
  "type":"object","additionalProperties":false,"required":["sources"],
  "properties":{"sources":{"type":"array","items":{
    "type":"object","additionalProperties":false,
    "required":["candidate_index","classification","owner","claim_indices"],
    "properties":{
      "candidate_index":{"type":"integer","minimum":0},
      "classification":{"type":"string","enum":["competitor_owned","third_party","unknown"]},
      "owner":{"type":"string"},
      "claim_indices":{"type":"array","items":{"type":"integer","minimum":0}}
    }
  }}}
}`

var sourceClassificationSchema = mustParseJSONSchema(sourceClassificationJSONSchema)

type OpenAISourceClassifier struct{ openAIResponsesClient }

func NewOpenAISourceClassifier(cfg OpenAIConfig) (*OpenAISourceClassifier, error) {
	client, err := newOpenAIResponsesClient(cfg, "openai analysis model is required")
	if err != nil {
		return nil, err
	}
	return &OpenAISourceClassifier{openAIResponsesClient: client}, nil
}

func (r *OpenAISourceClassifier) ClassifySources(ctx context.Context, in SourceClassificationInput) ([]SourceClassification, error) {
	if r == nil {
		return nil, errors.New("openai source classifier is nil")
	}
	if len(in.Candidates) == 0 {
		return []SourceClassification{}, nil
	}
	return classifySourcesWithRetry(ctx, r, in)
}

func (r *OpenAISourceClassifier) runSourceClassification(ctx context.Context, in SourceClassificationInput) (SourceClassificationRunResult, error) {
	content, err := json.Marshal(struct {
		Candidates []SourceCandidate `json:"candidates"`
	}{Candidates: in.Candidates})
	if err != nil {
		return SourceClassificationRunResult{}, fmt.Errorf("marshal source classification input: %w", err)
	}
	input := responses.ResponseInputParam{
		responses.ResponseInputItemParamOfMessage(sourceClassificationInstructions, responses.EasyInputMessageRoleDeveloper),
		responses.ResponseInputItemParamOfMessage(string(content), responses.EasyInputMessageRoleUser),
	}
	if len(in.PriorOutputJSON) > 0 || len(in.RetryValidationErrors) > 0 {
		input = append(input,
			responses.ResponseInputItemParamOfMessage(string(in.PriorOutputJSON), responses.EasyInputMessageRoleAssistant),
			responses.ResponseInputItemParamOfMessage(retryContent(in.RetryValidationErrors), responses.EasyInputMessageRoleUser),
		)
	}
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(r.model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store: openai.Bool(false),
		Text:  responses.ResponseTextConfigParam{Format: strictJSONSchemaFormat(openAISourceClassificationSchemaName, sourceClassificationSchema)},
	}
	parsed, body, err := r.call(ctx, params)
	if err != nil {
		return SourceClassificationRunResult{}, err
	}
	if err := validateOpenAIResponse(parsed, body, "openai source classification response missing reported model", "openai source classification response missing structured output"); err != nil {
		return SourceClassificationRunResult{}, err
	}
	return SourceClassificationRunResult{RawJSON: json.RawMessage(parsed.Text), Model: strings.TrimSpace(parsed.Model)}, nil
}
