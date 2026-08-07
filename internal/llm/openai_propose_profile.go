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

const openAIProposeProfileSchemaName = "profile_proposal"

// proposeProfileInstructions is the developer-role prompt encoding the design
// 03 combined research+draft rules. Customer questions are a separate,
// non-research call (see questionsInstructions in openai_questions.go) fired
// on demand after the user reviews Services, so this prompt covers only the
// business profile.
const proposeProfileInstructions = `Research and draft a business profile proposal for a specialist clinic or practice. The owner will review it before it is saved. Use web search and agentic browsing, and do not invent unsupported facts.

EVIDENCE
- Treat site_text as the primary evidence. If it is empty or insufficient, open the supplied website directly. Use web search to verify the business's identity, category, services, location, organization aliases, and relevant directory or registry listings.
- Prefer the business's own website for claims about the business. Use other sources to verify or fill gaps.
- Set low_confidence to true if any material profile field is guessed or weakly supported. Set it false only when the evidence clearly supports the whole profile.

PROFILE
- Use only organization trading identities as aliases, such as former, foreign-language, colloquial, abbreviated, or directory-listing names. Do not use a practitioner's personal name unless it is part of the organization's trading name. Return an empty array when there are no supported aliases.
- Make category a concise specialist category a prospective patient would search for. Derive it from evidence about this business; do not default to a common category when evidence is thin.
- Include only services supported by the evidence.
- location.country must be a two-letter ISO 3166-1 alpha-2 code. If it must be inferred from contextual evidence, set low_confidence to true.

RETRY
- If prior output and validation failures are provided, fix every listed issue and return the full corrected object, not a diff.`

// proposeProfileJSONSchema is the strict-mode structured-output schema. Strict
// mode does not support array length keywords, so the exact prompt count is
// enforced only by ValidateProposal (same posture as extraction/match not
// encoding cardinality in the schema).
const proposeProfileJSONSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["low_confidence", "profile"],
  "properties": {
    "low_confidence": {"type": "boolean"},
    "profile": {
      "type": "object",
      "additionalProperties": false,
      "required": ["name", "aliases", "category", "services", "location"],
      "properties": {
        "name": {"type": "string"},
        "aliases": {"type": "array", "items": {"type": "string"}},
        "category": {"type": "string"},
        "services": {"type": "array", "items": {"type": "string"}},
        "location": {
          "type": "object",
          "additionalProperties": false,
          "required": ["address", "area", "city", "country"],
          "properties": {
            "address": {"type": "string"},
            "area": {"type": "string"},
            "city": {"type": "string"},
            "country": {"type": "string"}
          }
        }
      }
    }
  }
}`

var proposeProfileSchema = mustParseJSONSchema(proposeProfileJSONSchema)

// OpenAIProposeProfileRunner runs the proposal call through the OpenAI Responses
// API. Same field shape as OpenAIExtractionRunner.
type OpenAIProposeProfileRunner struct {
	openAIResponsesClient
}

// NewOpenAIProposeProfileRunner returns an OpenAI-backed ProposeProfileRunner.
func NewOpenAIProposeProfileRunner(cfg OpenAIConfig) (*OpenAIProposeProfileRunner, error) {
	client, err := newOpenAIResponsesClient(cfg, "openai analysis model is required")
	if err != nil {
		return nil, err
	}
	return &OpenAIProposeProfileRunner{openAIResponsesClient: client}, nil
}

// RunProposeProfile sends one combined research+draft call: the web_search tool
// is attached (tool_choice: required) so the model gathers its own evidence, and
// the strict JSON schema forces schema-conforming output. It returns the model's
// JSON text (decoded by the caller), the full raw response body (for Sources and
// web-search-action detection), and the reported model id. Provider failures
// reuse the same transient/non-retryable RunnerError classification as
// extraction.
func (r *OpenAIProposeProfileRunner) RunProposeProfile(ctx context.Context, in ProposeProfileInput) (ProposeProfileRunResult, error) {
	if r == nil {
		return ProposeProfileRunResult{}, errors.New("openai propose profile runner is nil")
	}

	params, err := r.requestParams(in)
	if err != nil {
		return ProposeProfileRunResult{}, err
	}
	parsed, body, err := r.call(ctx, params)
	if err != nil {
		return ProposeProfileRunResult{}, err
	}
	if err := validateOpenAIResponse(parsed, body, "openai propose profile response missing reported model", "openai propose profile response missing structured output"); err != nil {
		return ProposeProfileRunResult{}, err
	}

	return ProposeProfileRunResult{
		RawJSON:     json.RawMessage(parsed.Text),
		RawResponse: append(json.RawMessage(nil), body...),
		Model:       strings.TrimSpace(parsed.Model),
	}, nil
}

func (r *OpenAIProposeProfileRunner) requestParams(in ProposeProfileInput) (responses.ResponseNewParams, error) {
	userContent, err := marshalProposeProfileUserContent(in)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}

	input := responses.ResponseInputParam{
		responses.ResponseInputItemParamOfMessage(proposeProfileInstructions, responses.EasyInputMessageRoleDeveloper),
		responses.ResponseInputItemParamOfMessage(userContent, responses.EasyInputMessageRoleUser),
	}
	if len(in.PriorOutputJSON) > 0 || len(in.RetryValidationErrors) > 0 {
		input = append(input,
			responses.ResponseInputItemParamOfMessage(string(in.PriorOutputJSON), responses.EasyInputMessageRoleAssistant),
			responses.ResponseInputItemParamOfMessage(retryContent(in.RetryValidationErrors), responses.EasyInputMessageRoleUser),
		)
	}

	return responses.ResponseNewParams{
		Model: shared.ResponsesModel(r.model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store: openai.Bool(false),
		Tools: []responses.ToolUnionParam{webSearchTool(in.Location)},
		// The spike showed tool_choice "required" drives one or two search/open_page
		// actions per run with no loops or errors, so we force at least one search
		// rather than relying on the model's discretion.
		ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsRequired),
		},
		Text: responses.ResponseTextConfigParam{
			Format: strictJSONSchemaFormat(openAIProposeProfileSchemaName, proposeProfileSchema),
		},
	}, nil
}

// marshalProposeProfileUserContent serialises the evidence the model reads.
func marshalProposeProfileUserContent(in ProposeProfileInput) (string, error) {
	payload := struct {
		BusinessName string `json:"business_name"`
		Website      string `json:"website"`
		SiteText     string `json:"site_text"`
	}{
		BusinessName: in.Name,
		Website:      in.Website,
		SiteText:     in.SiteText,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal propose profile user content: %w", err)
	}
	return string(raw), nil
}
