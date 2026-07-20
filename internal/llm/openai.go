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

const (
	defaultOpenAIBaseURL              = "https://api.openai.com/v1"
	openAIResponsesPath               = "/responses"
	openAIWebSearchToolType           = "web_search"
	openAISearchContextSize           = "medium"
	openAIApproximateLocationType     = "approximate"
	maxOpenAIResponseBodyBytes    int = 8 << 20
)

// OpenAIConfig configures the OpenAI Responses PromptRunner.
type OpenAIConfig struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

// OpenAIPromptRunner executes prompts through the OpenAI Responses API.
type OpenAIPromptRunner struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIPromptRunner returns an OpenAI-backed PromptRunner.
func NewOpenAIPromptRunner(cfg OpenAIConfig) (*OpenAIPromptRunner, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("openai api key is required")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("openai responses model is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OpenAIPromptRunner{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: httpClient,
	}, nil
}

// RunPrompt sends the user's prompt as the only instruction and returns both
// the exact request JSON and the raw response JSON for persistence.
func (r *OpenAIPromptRunner) RunPrompt(ctx context.Context, req PromptRequest) (PromptRunResult, error) {
	if r == nil {
		return PromptRunResult{}, errors.New("openai prompt runner is nil")
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return PromptRunResult{}, errors.New("prompt is required")
	}
	location := req.Location.normalized()
	if err := location.validate(); err != nil {
		return PromptRunResult{}, err
	}

	requestJSON, err := r.requestJSON(prompt, location)
	if err != nil {
		return PromptRunResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		r.baseURL+openAIResponsesPath,
		bytes.NewReader(requestJSON),
	)
	if err != nil {
		return PromptRunResult{}, fmt.Errorf("build openai request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return PromptRunResult{}, fmt.Errorf("call openai responses: %w", err)
	}
	defer func() {
		_ = httpResp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, int64(maxOpenAIResponseBodyBytes)+1))
	if err != nil {
		return PromptRunResult{}, fmt.Errorf("read openai response: %w", err)
	}
	if len(body) > maxOpenAIResponseBodyBytes {
		return PromptRunResult{}, errors.New("openai response body exceeds size limit")
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return PromptRunResult{}, openAIHTTPError(httpResp.StatusCode, body)
	}

	parsed, err := parseOpenAIResponse(body)
	if err != nil {
		return PromptRunResult{}, err
	}
	if parsed.Status != "completed" {
		return PromptRunResult{}, incompleteOpenAIResponseError(parsed, body)
	}
	if parsed.Refusal != "" {
		return PromptRunResult{}, &RunnerError{
			Type:         "content_policy_refusal",
			Message:      parsed.Refusal,
			Body:         append(json.RawMessage(nil), body...),
			nonRetryable: true,
		}
	}
	if strings.TrimSpace(parsed.Model) == "" {
		return PromptRunResult{}, errors.New("openai response missing reported model")
	}
	if strings.TrimSpace(parsed.Text) == "" {
		return PromptRunResult{}, errors.New("openai response missing output_text")
	}

	return PromptRunResult{
		RequestJSON:  append(json.RawMessage(nil), requestJSON...),
		RawResponse:  append(json.RawMessage(nil), body...),
		ResponseText: parsed.Text,
		Model:        strings.TrimSpace(parsed.Model),
	}, nil
}

func (r *OpenAIPromptRunner) requestJSON(prompt string, location Location) (json.RawMessage, error) {
	payload := openAIResponseRequest{
		Model: r.model,
		Input: prompt,
		Store: false,
		Tools: []openAIWebSearchTool{{
			Type:              openAIWebSearchToolType,
			SearchContextSize: openAISearchContextSize,
			UserLocation: openAIUserLocation{
				Type:     openAIApproximateLocationType,
				Country:  location.Country,
				City:     location.City,
				Region:   location.Region,
				Timezone: location.Timezone,
			},
		}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal openai request: %w", err)
	}
	return raw, nil
}

type openAIResponseRequest struct {
	Model string                `json:"model"`
	Input string                `json:"input"`
	Store bool                  `json:"store"`
	Tools []openAIWebSearchTool `json:"tools"`
}

type openAIWebSearchTool struct {
	Type              string             `json:"type"`
	SearchContextSize string             `json:"search_context_size"`
	UserLocation      openAIUserLocation `json:"user_location"`
}

type openAIUserLocation struct {
	Type     string `json:"type"`
	Country  string `json:"country"`
	City     string `json:"city,omitempty"`
	Region   string `json:"region,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type openAIParsedResponse struct {
	Model             string
	Status            string
	IncompleteDetails json.RawMessage
	Text              string
	Refusal           string
}

func parseOpenAIResponse(body []byte) (openAIParsedResponse, error) {
	var payload struct {
		Model             string          `json:"model"`
		Status            string          `json:"status"`
		IncompleteDetails json.RawMessage `json:"incomplete_details"`
		Output            []struct {
			Type    string `json:"type"`
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		} `json:"output"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return openAIParsedResponse{}, fmt.Errorf("parse openai response: %w", err)
	}
	if payload.Error != nil && payload.Error.Message != "" {
		return openAIParsedResponse{}, &RunnerError{
			Type:         payload.Error.Type,
			Code:         payload.Error.Code,
			Message:      payload.Error.Message,
			Body:         append(json.RawMessage(nil), body...),
			nonRetryable: false,
		}
	}

	var parts []string
	var refusals []string
	for _, output := range payload.Output {
		if output.Type != "message" {
			continue
		}
		for _, content := range output.Content {
			switch content.Type {
			case "output_text":
				if strings.TrimSpace(content.Text) != "" {
					parts = append(parts, content.Text)
				}
			case "refusal":
				if strings.TrimSpace(content.Refusal) != "" {
					refusals = append(refusals, content.Refusal)
				}
			}
		}
	}

	return openAIParsedResponse{
		Model:             payload.Model,
		Status:            payload.Status,
		IncompleteDetails: append(json.RawMessage(nil), payload.IncompleteDetails...),
		Text:              strings.Join(parts, "\n"),
		Refusal:           strings.Join(refusals, "\n"),
	}, nil
}

func incompleteOpenAIResponseError(parsed openAIParsedResponse, body []byte) error {
	status := strings.TrimSpace(parsed.Status)
	if status == "" {
		status = "missing"
	}
	message := fmt.Sprintf("response status %q", status)
	if len(parsed.IncompleteDetails) > 0 && string(parsed.IncompleteDetails) != "null" {
		message += ": " + string(parsed.IncompleteDetails)
	}
	return &RunnerError{
		Type:         "response_" + status,
		Message:      message,
		Body:         append(json.RawMessage(nil), body...),
		nonRetryable: incompleteDetailsLookPolicyFiltered(parsed.IncompleteDetails),
	}
}

func incompleteDetailsLookPolicyFiltered(raw json.RawMessage) bool {
	lower := strings.ToLower(string(raw))
	return strings.Contains(lower, "content_filter") ||
		strings.Contains(lower, "content_policy") ||
		strings.Contains(lower, "safety")
}

func openAIHTTPError(statusCode int, body []byte) error {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)

	message := strings.TrimSpace(parsed.Error.Message)
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return &RunnerError{
		StatusCode:   statusCode,
		Type:         parsed.Error.Type,
		Code:         parsed.Error.Code,
		Message:      message,
		Body:         append(json.RawMessage(nil), body...),
		nonRetryable: statusCode >= 400 && statusCode < 500 && statusCode != http.StatusTooManyRequests,
	}
}
