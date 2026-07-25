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

// proposeProfileInstructions is the developer-role prompt encoding the design 03
// combined research+draft rules and "Prompt generation rules". Prompt count is
// never hardcoded — it is passed as prompt_count in the user content. This call
// has the web_search tool attached (tool_choice: required), so the model gathers
// its own evidence rather than being handed a separate research summary.
const proposeProfileInstructions = `Research and draft a business profile proposal for a clinic/practice. The business owner will review it before anything is saved. Use web search and agentic browsing to gather evidence. Do not invent facts beyond what site_text and your web research support.

RESEARCH (do this before drafting)
- site_text is the primary evidence: the business's own website, pre-fetched for you. It may be empty or thin (some sites render nothing without JavaScript, which the fetcher cannot run).
- The business website URL is given as "website". If site_text is empty or thin, OPEN that URL directly and read it yourself — this is the single most reliable source. When there is no site evidence at all, web search (including opening the site and directory pages) is your only evidence, so search thoroughly.
- Search the web to (1) confirm what the business is and its specialty/category, (2) find organization-only aliases (former, foreign-language e.g. Chinese, or colloquial/abbreviated TRADING names — never a practitioner's personal name unless it is genuinely part of the trading name), and (3) find directory/profile listings (Google, health directories, professional registries, review sites).
- Set low_confidence to true whenever you had to guess a value with weak or no supporting evidence (most commonly: location.country, category, aliases). For category specifically: if neither site_text, your research, nor the business name itself states what this business does, any category you output is a guess — set low_confidence true. Set it false only when the evidence clearly supports the whole profile.

PROFILE
- aliases: OTHER organization trading identities only — former names, foreign-language names, colloquial/abbreviated names, directory-listing names. NEVER a practitioner's personal name, unless it is genuinely part of the trading name itself (e.g. "Dr Lim's Family Clinic"). Empty array if none found.
- category: the specialist category a prospective patient would search for (examples across specialties: "endodontic clinic", "orthopaedic clinic", "aesthetic skin clinic"). It MUST be derived from evidence about THIS business — never copy an example and never default to a common category when evidence is thin. The business name itself is strong evidence when it contains a medical/dental specialty term: a name containing "Endodontics" means an endodontic (root canal) dental clinic, "Dermatology" a dermatology clinic, and so on.
- location.country must be a two-letter ISO 3166-1 alpha-2 code. If evidence gives no explicit country, infer the best guess from address format, phone country code, domain TLD, currency, or language, and set low_confidence true.

PROMPTS (this is the product's core measurement instrument)
- Generate EXACTLY prompt_count prompts (given in the input; never hardcode a number).
- A prompt's text must NEVER contain the business name or any alias, in any form. Prompts simulate a prospective patient who does not know this business exists yet.
- Vary the set across broad category searches ("best <category> in <city>"), specific services/procedures, and symptom or problem descriptions. Ground prompts in the business's city only — never in a neighbourhood, district, street, or landmark within it.
- Phrase every prompt the way a real person types a question or problem to a chatbot — natural questions or problem statements, never a bare keyword string.

RETRY
- If prior output and validation failures are provided, they list exactly what was wrong. Fix all of them and re-emit the FULL corrected object, not a diff.`

// proposeProfileJSONSchema is the strict-mode structured-output schema. Strict
// mode does not support array length keywords, so the exact prompt count is
// enforced only by ValidateProposal (same posture as extraction/match not
// encoding cardinality in the schema).
const proposeProfileJSONSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["low_confidence", "profile", "prompts"],
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
    },
    "prompts": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["text"],
        "properties": {
          "text": {"type": "string"}
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
// prompt_count is in.PromptLimit (plan.prompt_limit — never hardcoded).
func marshalProposeProfileUserContent(in ProposeProfileInput) (string, error) {
	payload := struct {
		BusinessName string `json:"business_name"`
		Website      string `json:"website"`
		SiteText     string `json:"site_text"`
		PromptCount  int    `json:"prompt_count"`
	}{
		BusinessName: in.Name,
		Website:      in.Website,
		SiteText:     in.SiteText,
		PromptCount:  in.PromptLimit,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal propose profile user content: %w", err)
	}
	return string(raw), nil
}
