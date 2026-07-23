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
// step-3 rules and "Prompt generation rules". Prompt count is never hardcoded —
// it is passed as prompt_count in the user content.
const proposeProfileInstructions = `You build a structured business profile proposal for a clinic/practice, to be reviewed and edited by the business owner before anything is saved — nothing here is written until they approve it. Use ONLY the evidence given (site_text, research_summary); do not invent facts beyond what a careful, honest reading of that evidence supports. Return only the JSON object required by the schema.

EVIDENCE
- site_text and research_summary may each be empty, thin, or partially contradictory. research_summary is a free-text narrative report of a web search, not a list — read it as prose and extract whatever facts it actually states.
- Set low_confidence to true whenever you had to guess a value with weak or no supporting evidence (most commonly: location.country, category, aliases). Set it false only when the evidence clearly supports the whole profile.

PROFILE
- name: the business's primary trading name.
- aliases: OTHER organization trading identities only — former names, foreign-language names, colloquial/abbreviated names, directory-listing names. NEVER a practitioner's personal name, unless it is genuinely part of the trading name itself (e.g. "Dr Tan's Orthopaedic Practice"). Empty array if none found.
- category: the specialist category a patient would search for (e.g. "orthopaedic clinic").
- practitioners: doctors/specialists/staff, with role where known (empty string if unknown role). Empty array if none found.
- services: services/procedures offered, as short phrases. Empty array if none found.
- location: address/area/city as best known (empty string for any unknown part); country is ALWAYS a two-letter ISO 3166-1 alpha-2 code (e.g. "SG", "US") — never a full country name, never blank. If the evidence gives no explicit country, infer your best guess from address format, phone country code, domain TLD, currency, or language, and set low_confidence true.

PROMPTS (this is the product's core measurement instrument)
- Generate EXACTLY prompt_count prompts (given in the input; never hardcode a number).
- A prompt's text must NEVER contain the business name or any alias, in any form. Prompts simulate a prospective patient who does not know this business exists yet.
- Each prompt has a kind: category | service | condition | location.
  - category: "best <category> in <city>"-style.
  - service: asks about a specific service/procedure.
  - condition: describes a symptom/problem, not a diagnosis or service name.
  - location: anchored to a specific area/neighbourhood/landmark.
- Mix kinds across the full set: when prompt_count allows it, use all four kinds at least once, roughly evenly. Never fill a batch with a single kind.
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
      "required": ["name", "aliases", "category", "practitioners", "services", "location"],
      "properties": {
        "name": {"type": "string"},
        "aliases": {"type": "array", "items": {"type": "string"}},
        "category": {"type": "string"},
        "practitioners": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["name", "role"],
            "properties": {"name": {"type": "string"}, "role": {"type": "string"}}
          }
        },
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
        "required": ["text", "kind"],
        "properties": {
          "text": {"type": "string"},
          "kind": {"type": "string", "enum": ["category", "service", "condition", "location"]}
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

// RunProposeProfile sends one structured-output proposal call and returns the
// model's JSON text (decoded by the caller) plus the reported model id. This is
// pure structured-output reasoning over evidence already gathered by
// FetchSite/ResearchBusiness — no web_search tool. Provider failures reuse the
// same transient/non-retryable RunnerError classification as extraction.
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
		RawJSON: json.RawMessage(parsed.Text),
		Model:   strings.TrimSpace(parsed.Model),
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

	payload := openAIExtractionRequest{
		Model: r.model,
		Input: input,
		Store: false,
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

// marshalProposeProfileUserContent serialises the evidence the model reads.
// prompt_count is in.PromptLimit (plan.prompt_limit — never hardcoded).
func marshalProposeProfileUserContent(in ProposeProfileInput) (string, error) {
	payload := struct {
		BusinessName    string `json:"business_name"`
		Website         string `json:"website"`
		SiteText        string `json:"site_text"`
		ResearchSummary string `json:"research_summary"`
		PromptCount     int    `json:"prompt_count"`
	}{
		BusinessName:    in.Name,
		Website:         in.Website,
		SiteText:        in.SiteText,
		ResearchSummary: in.ResearchSummary,
		PromptCount:     in.PromptLimit,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal propose profile user content: %w", err)
	}
	return string(raw), nil
}
