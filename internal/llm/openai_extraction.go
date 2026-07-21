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

// ExtractionPromptVersion is the version of the extraction prompt + schema,
// recorded per row as result_analyses.extraction_version so a later pass can
// target "re-analyze everything below version N" (design 05). Bump it in the
// same commit as any change to extractionInstructions or extractionJSONSchema.
const ExtractionPromptVersion = 2

const openAIExtractionSchemaName = "result_extraction"

// extractionInstructions is the developer-role prompt. It encodes the design 05
// extraction rules; the literal phrasing may be iterated by the quality gate,
// but the set of rules encoded is fixed. Any edit here bumps
// ExtractionPromptVersion.
const extractionInstructions = `You extract structured facts from a single AI assistant response that answered a consumer's question about local businesses. You never browse, fetch, or infer beyond the response text you are given. Return only the JSON object required by the schema.

ENTITIES (organizations only)
- List every ORGANISATION the response recommends or discusses — clinics, practices, hospitals, companies. Never list an individual practitioner or employee (e.g. "Dr Tan Wei Ming"), and never list directories, review sites, aggregators, or government bodies; those are citation sources, not entities.
- A recommendation that names only a person, with no organisation, yields NO entity for that recommendation. Practitioner-only mentions are not business mentions.
- List entities in order of their first appearance in the response text.
- verbatim_name and excerpt must each be an exact, character-for-character quote copied from the response text (only incidental whitespace may differ). The excerpt is the sentence where the entity first appears. Do not paraphrase, translate, correct, or fabricate.
- When writing verbatim_name and excerpt strings, output ordinary punctuation (including &) as literal characters — never as a \uXXXX escape sequence.
- is_target: true only if the entity is the target business described below (judge using its name and aliases). This is a hint; when unsure, set false.

TARGET
- Set target to null if the target business is not mentioned at all.
- Otherwise, sentiment/keywords describe HOW THE RESPONSE CHARACTERISES THE TARGET BUSINESS specifically — not the response's overall tone. keywords are the recurring descriptors or themes applied to the target.
- Every keyword and the sentiment must be supportable by one of the excerpts. Each excerpt must be an exact quote from the response text (only incidental whitespace may differ).

CITATIONS
- For each supplied citation, judge its subject FROM THE RESPONSE'S OWN SURROUNDING TEXT ONLY — never by guessing what the page contains: "business" if it supports the target business, "competitor" if it supports another organisation, "other" if neither, "unknown" when the surrounding text does not make it clear. "unknown" is the honest default, not a failure.
- Return citations in order of first appearance in the response text.

RETRY
- If prior output and validation failures are provided, they list exactly what was wrong. Fix all of them and re-emit the FULL corrected object, not a diff.`

// extractionJSONSchema is the strict-mode structured-output schema (design 05).
// Nullability is expressed via "type":["object","null"] because strict mode
// requires every property to be present and every object to forbid extras.
const extractionJSONSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["entities", "target", "citations"],
  "properties": {
    "entities": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["verbatim_name", "is_target", "excerpt"],
        "properties": {
          "verbatim_name": {"type": "string"},
          "is_target": {"type": "boolean"},
          "excerpt": {"type": "string"}
        }
      }
    },
    "target": {
      "type": ["object", "null"],
      "additionalProperties": false,
      "required": ["sentiment", "keywords", "excerpts"],
      "properties": {
        "sentiment": {"type": "string", "enum": ["positive", "neutral", "negative", "mixed"]},
        "keywords": {"type": "array", "items": {"type": "string"}},
        "excerpts": {"type": "array", "items": {"type": "string"}}
      }
    },
    "citations": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["url", "subject"],
        "properties": {
          "url": {"type": "string"},
          "subject": {"type": "string", "enum": ["business", "competitor", "other", "unknown"]}
        }
      }
    }
  }
}`

var extractionSchema = mustParseJSONSchema(extractionJSONSchema)

func mustParseJSONSchema(s string) json.RawMessage {
	if !json.Valid([]byte(s)) {
		panic("extraction json schema is not valid JSON")
	}
	return json.RawMessage(s)
}

// OpenAIExtractionRunner runs the extraction call through the OpenAI Responses
// API. Same field shape as OpenAIPromptRunner.
type OpenAIExtractionRunner struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIExtractionRunner returns an OpenAI-backed ExtractionRunner.
func NewOpenAIExtractionRunner(cfg OpenAIConfig) (*OpenAIExtractionRunner, error) {
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
	return &OpenAIExtractionRunner{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: httpClient,
	}, nil
}

// RunExtraction sends one structured-output extraction call and returns the
// model's JSON text (unmarshalled by the caller) plus the reported model id.
// Provider failures reuse the same transient/non-retryable RunnerError
// classification as the execution runner.
func (r *OpenAIExtractionRunner) RunExtraction(ctx context.Context, in ExtractionInput) (ExtractionRunResult, error) {
	if r == nil {
		return ExtractionRunResult{}, errors.New("openai extraction runner is nil")
	}
	if strings.TrimSpace(in.ResponseText) == "" {
		return ExtractionRunResult{}, errors.New("response text is required")
	}

	requestJSON, err := r.requestJSON(in)
	if err != nil {
		return ExtractionRunResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		r.baseURL+openAIResponsesPath,
		bytes.NewReader(requestJSON),
	)
	if err != nil {
		return ExtractionRunResult{}, fmt.Errorf("build openai extraction request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return ExtractionRunResult{}, fmt.Errorf("call openai extraction: %w", err)
	}
	defer func() {
		_ = httpResp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, int64(maxOpenAIResponseBodyBytes)+1))
	if err != nil {
		return ExtractionRunResult{}, fmt.Errorf("read openai extraction response: %w", err)
	}
	if len(body) > maxOpenAIResponseBodyBytes {
		return ExtractionRunResult{}, errors.New("openai extraction response body exceeds size limit")
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return ExtractionRunResult{}, openAIHTTPError(httpResp.StatusCode, body)
	}

	parsed, err := parseOpenAIResponse(body)
	if err != nil {
		return ExtractionRunResult{}, err
	}
	if parsed.Status != "completed" {
		return ExtractionRunResult{}, incompleteOpenAIResponseError(parsed, body)
	}
	if parsed.Refusal != "" {
		return ExtractionRunResult{}, &RunnerError{
			Type:         "content_policy_refusal",
			Message:      parsed.Refusal,
			Body:         append(json.RawMessage(nil), body...),
			nonRetryable: true,
		}
	}
	if strings.TrimSpace(parsed.Model) == "" {
		return ExtractionRunResult{}, errors.New("openai extraction response missing reported model")
	}
	if strings.TrimSpace(parsed.Text) == "" {
		return ExtractionRunResult{}, errors.New("openai extraction response missing structured output")
	}

	return ExtractionRunResult{
		RawJSON: json.RawMessage(parsed.Text),
		Model:   strings.TrimSpace(parsed.Model),
	}, nil
}

func (r *OpenAIExtractionRunner) requestJSON(in ExtractionInput) (json.RawMessage, error) {
	userContent, err := marshalExtractionUserContent(in)
	if err != nil {
		return nil, err
	}

	input := []openAIExtractionMessage{
		{Role: "developer", Content: extractionInstructions},
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
				Name:   openAIExtractionSchemaName,
				Schema: extractionSchema,
				Strict: true,
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal openai extraction request: %w", err)
	}
	return raw, nil
}

// marshalExtractionUserContent serialises the analysis payload the model reads.
func marshalExtractionUserContent(in ExtractionInput) (string, error) {
	type citationView struct {
		URL        string `json:"url"`
		Title      string `json:"title"`
		StartIndex int    `json:"start_index"`
		EndIndex   int    `json:"end_index"`
	}
	citations := make([]citationView, 0, len(in.Citations))
	for _, c := range in.Citations {
		citations = append(citations, citationView{
			URL:        c.URL,
			Title:      c.Title,
			StartIndex: c.StartIndex,
			EndIndex:   c.EndIndex,
		})
	}
	aliases := in.BusinessAliases
	if aliases == nil {
		aliases = []string{}
	}

	payload := struct {
		Prompt         string `json:"prompt"`
		ResponseText   string `json:"response_text"`
		TargetBusiness struct {
			Name     string   `json:"name"`
			Aliases  []string `json:"aliases"`
			Category string   `json:"category"`
			Location string   `json:"location"`
		} `json:"target_business"`
		Citations []citationView `json:"citations"`
	}{
		Prompt:       in.Prompt,
		ResponseText: in.ResponseText,
		Citations:    citations,
	}
	payload.TargetBusiness.Name = in.BusinessName
	payload.TargetBusiness.Aliases = aliases
	payload.TargetBusiness.Category = in.BusinessCategory
	payload.TargetBusiness.Location = in.BusinessLocation

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal extraction user content: %w", err)
	}
	return string(raw), nil
}

func retryContent(validationErrors []string) string {
	var b strings.Builder
	b.WriteString("Your previous output failed these validation checks:\n")
	for _, e := range validationErrors {
		b.WriteString("- ")
		b.WriteString(e)
		b.WriteString("\n")
	}
	b.WriteString("Fix all issues and re-emit the full corrected object.")
	return b.String()
}

type openAIExtractionRequest struct {
	Model string                    `json:"model"`
	Input []openAIExtractionMessage `json:"input"`
	Store bool                      `json:"store"`
	Text  openAIResponseTextFormat  `json:"text"`
}

type openAIExtractionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponseTextFormat struct {
	Format openAIJSONSchemaFormat `json:"format"`
}

type openAIJSONSchemaFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}
