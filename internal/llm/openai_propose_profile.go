package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const openAIProposeProfileSchemaName = "profile_proposal"

// proposeProfileInstructions is the developer-role prompt encoding the design 03
// combined research+draft rules and "Prompt generation rules". Prompt count is
// never hardcoded — it is passed as prompt_count in the user content. This call
// has the web_search tool attached (tool_choice: required), so the model gathers
// its own evidence rather than being handed a separate research summary.
const proposeProfileInstructions = `You build a structured business profile proposal for a clinic/practice, to be reviewed and edited by the business owner before anything is saved — nothing here is written until they approve it. You have a web_search tool with agentic browsing (you can open and read pages). Use it to gather evidence, then output ONLY the JSON object required by the schema. Do not invent facts beyond what site_text and your web research honestly support.

RESEARCH (do this before drafting)
- site_text is the primary evidence: the business's own website, pre-fetched for you. It may be empty or thin (some sites render nothing without JavaScript, which the fetcher cannot run).
- The business website URL is given as "website". If site_text is empty or thin, OPEN that URL directly and read it yourself — this is the single most reliable source. When there is no site evidence at all, web search (including opening the site and directory pages) is your only evidence, so search thoroughly.
- Search the web to (1) confirm what the business is and its specialty/category, (2) find organization-only aliases (former, foreign-language e.g. Chinese, or colloquial/abbreviated TRADING names — never a practitioner's personal name unless it is genuinely part of the trading name), and (3) find directory/profile listings (Google, health directories, professional registries, review sites).
- Set low_confidence to true whenever you had to guess a value with weak or no supporting evidence (most commonly: location.country, category, aliases). For category specifically: if neither site_text, your research, nor the business name itself states what this business does, any category you output is a guess — set low_confidence true. Set it false only when the evidence clearly supports the whole profile.

PROFILE
- name: the business's primary trading name.
- aliases: OTHER organization trading identities only — former names, foreign-language names, colloquial/abbreviated names, directory-listing names. NEVER a practitioner's personal name, unless it is genuinely part of the trading name itself (e.g. "Dr Lim's Family Clinic"). Empty array if none found.
- category: the specialist category a prospective patient would search for (examples across specialties: "endodontic clinic", "orthopaedic clinic", "aesthetic skin clinic"). It MUST be derived from evidence about THIS business — never copy an example and never default to a common category when evidence is thin. The business name itself is strong evidence when it contains a medical/dental specialty term: a name containing "Endodontics" means an endodontic (root canal) dental clinic, "Dermatology" a dermatology clinic, and so on.
- services: services/procedures offered, as short phrases. Empty array if none found.
- location: address/area/city as best known (empty string for any unknown part); country is ALWAYS a two-letter ISO 3166-1 alpha-2 code (e.g. "SG", "US") — never a full country name, never blank. If the evidence gives no explicit country, infer your best guess from address format, phone country code, domain TLD, currency, or language, and set low_confidence true.

PROMPTS (this is the product's core measurement instrument)
- Generate EXACTLY prompt_count prompts (given in the input; never hardcode a number).
- A prompt's text must NEVER contain the business name or any alias, in any form. Prompts simulate a prospective patient who does not know this business exists yet.
- Vary the set across broad category searches ("best <category> in <city>"), specific services/procedures, symptom or problem descriptions, and searches anchored to an area/neighbourhood/landmark.
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
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIProposeProfileRunner returns an OpenAI-backed ProposeProfileRunner.
func NewOpenAIProposeProfileRunner(cfg OpenAIConfig) (*OpenAIProposeProfileRunner, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("openai api key is required")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("openai analysis model is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OpenAIProposeProfileRunner{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: httpClient,
	}, nil
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

	requestJSON, err := r.requestJSON(in)
	if err != nil {
		return ProposeProfileRunResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		r.baseURL+openAIResponsesPath,
		bytes.NewReader(requestJSON),
	)
	if err != nil {
		return ProposeProfileRunResult{}, fmt.Errorf("build openai propose profile request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return ProposeProfileRunResult{}, fmt.Errorf("call openai propose profile: %w", err)
	}
	defer func() {
		_ = httpResp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, int64(maxOpenAIResponseBodyBytes)+1))
	if err != nil {
		return ProposeProfileRunResult{}, fmt.Errorf("read openai propose profile response: %w", err)
	}
	if len(body) > maxOpenAIResponseBodyBytes {
		return ProposeProfileRunResult{}, errors.New("openai propose profile response body exceeds size limit")
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return ProposeProfileRunResult{}, openAIHTTPError(httpResp.StatusCode, body)
	}

	parsed, err := parseOpenAIResponse(body)
	if err != nil {
		return ProposeProfileRunResult{}, err
	}
	if parsed.Status != "completed" {
		return ProposeProfileRunResult{}, incompleteOpenAIResponseError(parsed, body)
	}
	if parsed.Refusal != "" {
		return ProposeProfileRunResult{}, &RunnerError{
			Type:         "content_policy_refusal",
			Message:      parsed.Refusal,
			Body:         append(json.RawMessage(nil), body...),
			nonRetryable: true,
		}
	}
	if strings.TrimSpace(parsed.Model) == "" {
		return ProposeProfileRunResult{}, errors.New("openai propose profile response missing reported model")
	}
	if strings.TrimSpace(parsed.Text) == "" {
		return ProposeProfileRunResult{}, errors.New("openai propose profile response missing structured output")
	}

	return ProposeProfileRunResult{
		RawJSON:     json.RawMessage(parsed.Text),
		RawResponse: append(json.RawMessage(nil), body...),
		Model:       strings.TrimSpace(parsed.Model),
	}, nil
}

func (r *OpenAIProposeProfileRunner) requestJSON(in ProposeProfileInput) (json.RawMessage, error) {
	userContent, err := marshalProposeProfileUserContent(in)
	if err != nil {
		return nil, err
	}

	input := []openAIExtractionMessage{
		{Role: "developer", Content: proposeProfileInstructions},
		{Role: "user", Content: userContent},
	}
	if len(in.PriorOutputJSON) > 0 || len(in.RetryValidationErrors) > 0 {
		input = append(input,
			openAIExtractionMessage{Role: "assistant", Content: string(in.PriorOutputJSON)},
			openAIExtractionMessage{Role: "user", Content: retryContent(in.RetryValidationErrors)},
		)
	}

	payload := openAIProposeProfileRequest{
		Model: r.model,
		Input: input,
		Store: false,
		Tools: []openAIWebSearchTool{{
			Type:              openAIWebSearchToolType,
			SearchContextSize: openAISearchContextSize,
			UserLocation: openAIUserLocation{
				Type:     openAIApproximateLocationType,
				Country:  in.Location.Country,
				City:     in.Location.City,
				Region:   in.Location.Region,
				Timezone: in.Location.Timezone,
			},
		}},
		// The spike showed tool_choice "required" drives one or two search/open_page
		// actions per run with no loops or errors, so we force at least one search
		// rather than relying on the model's discretion.
		ToolChoice: "required",
		Text: openAIResponseTextFormat{
			Format: openAIJSONSchemaFormat{
				Type:   "json_schema",
				Name:   openAIProposeProfileSchemaName,
				Schema: proposeProfileSchema,
				Strict: true,
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal openai propose profile request: %w", err)
	}
	return raw, nil
}

// openAIProposeProfileRequest is the combined research+draft request: the
// extraction-style message input and strict JSON schema, plus the web_search
// tool and a forced tool_choice so the model always gathers evidence.
type openAIProposeProfileRequest struct {
	Model      string                    `json:"model"`
	Input      []openAIExtractionMessage `json:"input"`
	Store      bool                      `json:"store"`
	Tools      []openAIWebSearchTool     `json:"tools"`
	ToolChoice string                    `json:"tool_choice"`
	Text       openAIResponseTextFormat  `json:"text"`
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
