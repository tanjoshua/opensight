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

const openAIMatchSchemaName = "competitor_match"

// matchInstructions is the developer-role prompt for the run's single match call
// (design 05 step 3). The bar is deliberately conservative: a wrong merge
// silently pollutes a competitor's trend, while a wrong split is visible and
// user-fixable, so the model defaults to "no match" unless the evidence is
// strong.
const matchInstructions = `You decide whether each unmatched business name refers to the SAME real-world business as one of a supplied list of known competitors. You judge only from the name, aliases, and website supplied for each competitor — you never browse or infer beyond them. Return only the JSON object required by the schema.

INPUT
- names: a list of unmatched business names, each addressed by its integer index.
- competitors: the known competitors, each with an id, name, aliases, and (sometimes) a website.

RULES
- For each name index, decide if it is the SAME real-world business as exactly one competitor. Set competitor_id to that competitor's id ONLY when the evidence is strong (e.g. an obvious abbreviation, a spelling/spacing variant, or a website that plainly belongs to the same business). Otherwise set competitor_id to null.
- When in doubt, return null. A wrong merge is worse than a missed one: it silently corrupts a competitor's history, while a missed match simply creates a new entry the user can merge later.
- competitor_id must be copied exactly from the supplied competitor list. Never invent an id and never guess.
- Emit exactly one entry per name index. Do not add, drop, or reorder indices.`

const matchJSONSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["matches"],
  "properties": {
    "matches": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["index", "competitor_id"],
        "properties": {
          "index": {"type": "integer"},
          "competitor_id": {"type": ["string", "null"]}
        }
      }
    }
  }
}`

var matchSchema = mustParseJSONSchema(matchJSONSchema)

// OpenAIMatchRunner runs the match call through the OpenAI Responses API. Same
// field shape as OpenAIExtractionRunner.
type OpenAIMatchRunner struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIMatchRunner returns an OpenAI-backed MatchRunner.
func NewOpenAIMatchRunner(cfg OpenAIConfig) (*OpenAIMatchRunner, error) {
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
	return &OpenAIMatchRunner{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: httpClient,
	}, nil
}

// RunMatch sends one structured-output match call and returns the model's JSON
// text (decoded by DecodeMatchOutput) plus the reported model id. Provider
// failures reuse the same RunnerError classification as the other runners.
func (r *OpenAIMatchRunner) RunMatch(ctx context.Context, in MatchInput) (MatchRunResult, error) {
	if r == nil {
		return MatchRunResult{}, errors.New("openai match runner is nil")
	}
	if len(in.Names) == 0 {
		return MatchRunResult{}, errors.New("match input has no names")
	}

	requestJSON, err := r.requestJSON(in)
	if err != nil {
		return MatchRunResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		r.baseURL+openAIResponsesPath,
		bytes.NewReader(requestJSON),
	)
	if err != nil {
		return MatchRunResult{}, fmt.Errorf("build openai match request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return MatchRunResult{}, fmt.Errorf("call openai match: %w", err)
	}
	defer func() {
		_ = httpResp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, int64(maxOpenAIResponseBodyBytes)+1))
	if err != nil {
		return MatchRunResult{}, fmt.Errorf("read openai match response: %w", err)
	}
	if len(body) > maxOpenAIResponseBodyBytes {
		return MatchRunResult{}, errors.New("openai match response body exceeds size limit")
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return MatchRunResult{}, openAIHTTPError(httpResp.StatusCode, body)
	}

	parsed, err := parseOpenAIResponse(body)
	if err != nil {
		return MatchRunResult{}, err
	}
	if parsed.Status != "completed" {
		return MatchRunResult{}, incompleteOpenAIResponseError(parsed, body)
	}
	if parsed.Refusal != "" {
		return MatchRunResult{}, &RunnerError{
			Type:         "content_policy_refusal",
			Message:      parsed.Refusal,
			Body:         append(json.RawMessage(nil), body...),
			nonRetryable: true,
		}
	}
	if strings.TrimSpace(parsed.Model) == "" {
		return MatchRunResult{}, errors.New("openai match response missing reported model")
	}
	if strings.TrimSpace(parsed.Text) == "" {
		return MatchRunResult{}, errors.New("openai match response missing structured output")
	}

	return MatchRunResult{
		RawJSON: json.RawMessage(parsed.Text),
		Model:   strings.TrimSpace(parsed.Model),
	}, nil
}

func (r *OpenAIMatchRunner) requestJSON(in MatchInput) (json.RawMessage, error) {
	userContent, err := marshalMatchUserContent(in)
	if err != nil {
		return nil, err
	}

	payload := openAIExtractionRequest{
		Model: r.model,
		Input: []openAIExtractionMessage{
			{Role: "developer", Content: matchInstructions},
			{Role: "user", Content: userContent},
		},
		Store: false,
		Text: openAIResponseTextFormat{
			Format: openAIJSONSchemaFormat{
				Type:   "json_schema",
				Name:   openAIMatchSchemaName,
				Schema: matchSchema,
				Strict: true,
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal openai match request: %w", err)
	}
	return raw, nil
}

// marshalMatchUserContent serialises the names (index-referenced) and the
// candidate competitor list the model reads.
func marshalMatchUserContent(in MatchInput) (string, error) {
	type nameView struct {
		Index int    `json:"index"`
		Name  string `json:"name"`
	}
	type competitorView struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Aliases []string `json:"aliases"`
		Website string   `json:"website,omitempty"`
	}

	names := make([]nameView, 0, len(in.Names))
	for i, n := range in.Names {
		names = append(names, nameView{Index: i, Name: n})
	}
	competitors := make([]competitorView, 0, len(in.Competitors))
	for _, c := range in.Competitors {
		aliases := c.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		competitors = append(competitors, competitorView{
			ID:      c.ID.String(),
			Name:    c.Name,
			Aliases: aliases,
			Website: c.Website,
		})
	}

	raw, err := json.Marshal(struct {
		Names       []nameView       `json:"names"`
		Competitors []competitorView `json:"competitors"`
	}{Names: names, Competitors: competitors})
	if err != nil {
		return "", fmt.Errorf("marshal match user content: %w", err)
	}
	return string(raw), nil
}
