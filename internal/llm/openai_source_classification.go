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

const sourceClassificationInstructions = `Analyze the inspected sources and customer-site material in one batch.

Classify every candidate as competitor_owned, third_party, or unknown. A competitor-owned source is official first-party content of exactly one linked competitor; establish ownership from inspected page identity or content, not a domain name alone. Emit each candidate_index exactly once. For competitor_owned, copy the matching linked owner and select only that owner's concrete claims. For other classifications, use owner "" and claim_indices [].

Then identify useful content opportunities. An opportunity is a topic that monitored answers explicitly used as a positive consideration for competitors and that the supplied customer pages do not communicate equally clearly. These are hypotheses from observed answer patterns, not established causes of inclusion or omission. Never promise that publishing something will improve rankings or visibility.

Exercise judgment against this rubric:
- Direct: the selected answer passages use the topic as a meaningful positive consideration, not an incidental fact.
- Comparable: the same topic can fairly be evaluated in the supplied customer-site material.
- Grounded: observation describes only the selected answers; site_state describes only the bounded pages supplied.
- Proportionate: suggested_action does not add facts or operational detail unsupported by the evidence.
- Useful: the information would help a prospective customer make a decision.

Omit an opportunity that fails the rubric or when site_content is empty. Group claims only when they support one coherent publishing job. Reuse a prior topic_key for the same enduring job; otherwise create a specific 3-64 character lowercase ASCII slug. Use a neutral title. Keep observation, site_state, and suggested_action distinct. Describe the site as "the pages we read", not the complete site. Use partial with one to three exact site passages when related information exists but lacks important detail; use absent with no site evidence otherwise. Each evidence reference must explain the exact supported_point. Phrase unknown customer facts conditionally and never recommend copying competitors or publishing unsubstantiated facts or outcomes.

If prior output and validation failures are supplied, fix every failure and emit the full corrected object.`

const sourceClassificationJSONSchema = `{
  "type":"object","additionalProperties":false,"required":["sources","content_opportunities"],
  "properties":{
    "sources":{"type":"array","items":{
    "type":"object","additionalProperties":false,
    "required":["candidate_index","classification","owner","claim_indices"],
    "properties":{
      "candidate_index":{"type":"integer","minimum":0},
      "classification":{"type":"string","enum":["competitor_owned","third_party","unknown"]},
      "owner":{"type":"string"},
      "claim_indices":{"type":"array","items":{"type":"integer","minimum":0}}
    }
  }},
    "content_opportunities":{"type":"array","maxItems":8,"items":{
      "type":"object","additionalProperties":false,
      "required":["topic_key","title","observation","site_state","suggested_action","coverage","site_evidence","evidence"],
      "properties":{
        "topic_key":{"type":"string","minLength":3,"maxLength":64,"pattern":"^[a-z0-9]+(?:-[a-z0-9]+)*$"},
        "title":{"type":"string"},
        "observation":{"type":"string"},
        "site_state":{"type":"string"},
        "suggested_action":{"type":"string"},
        "coverage":{"type":"string","enum":["absent","partial"]},
        "site_evidence":{"type":"array","maxItems":3,"items":{"type":"string"}},
        "evidence":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["candidate_index","claim_index","supported_point"],"properties":{"candidate_index":{"type":"integer","minimum":0},"claim_index":{"type":"integer","minimum":0},"supported_point":{"type":"string"}}}}
      }
    }}
  }
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

func (r *OpenAISourceClassifier) ClassifySources(ctx context.Context, in SourceClassificationInput) (SourceAnalysis, error) {
	if r == nil {
		return SourceAnalysis{}, errors.New("openai source classifier is nil")
	}
	if len(in.Candidates) == 0 {
		return SourceAnalysis{Sources: []SourceClassification{}, Opportunities: []ContentOpportunity{}}, nil
	}
	return classifySourcesWithRetry(ctx, r, in)
}

func (r *OpenAISourceClassifier) runSourceClassification(ctx context.Context, in SourceClassificationInput) (SourceClassificationRunResult, error) {
	content, err := json.Marshal(struct {
		BusinessName              string                    `json:"business_name"`
		SiteContent               string                    `json:"site_content"`
		PriorContentOpportunities []PriorContentOpportunity `json:"prior_content_opportunities"`
		Candidates                []SourceCandidate         `json:"candidates"`
	}{BusinessName: in.BusinessName, SiteContent: in.SiteContent, PriorContentOpportunities: in.PriorContentOpportunities, Candidates: in.Candidates})
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
