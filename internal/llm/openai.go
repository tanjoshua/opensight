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

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
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
	openAIResponsesClient
}

// NewOpenAIPromptRunner returns an OpenAI-backed PromptRunner.
func NewOpenAIPromptRunner(cfg OpenAIConfig) (*OpenAIPromptRunner, error) {
	client, err := newOpenAIResponsesClient(cfg, "openai responses model is required")
	if err != nil {
		return nil, err
	}
	return &OpenAIPromptRunner{openAIResponsesClient: client}, nil
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

	params := responsesParams(r.model, prompt, location)
	requestJSON, err := marshalOpenAIRequest(params, "openai request")
	if err != nil {
		return PromptRunResult{}, err
	}

	// From here on the request body exists, so every failure path carries
	// RequestJSON: ExecutePrompt persists a failed row whose NOT NULL
	// request column is this body, even when the provider fails.
	parsed, body, err := r.call(ctx, params)
	if err != nil {
		return PromptRunResult{RequestJSON: requestJSON}, err
	}
	if err := validateOpenAIResponse(parsed, body, "openai response missing reported model", "openai response missing output_text"); err != nil {
		return PromptRunResult{RequestJSON: requestJSON}, err
	}

	return PromptRunResult{
		RequestJSON:  append(json.RawMessage(nil), requestJSON...),
		RawResponse:  append(json.RawMessage(nil), body...),
		ResponseText: parsed.Text,
		Model:        strings.TrimSpace(parsed.Model),
	}, nil
}

// buildResponsesRequestJSON marshals the exact Responses API request body. It is
// shared by the OpenAI runner and the replay/stub runners so a persisted
// prompt_results.request has the same shape regardless of execution mode.
func buildResponsesRequestJSON(model, prompt string, location Location) (json.RawMessage, error) {
	return marshalOpenAIRequest(responsesParams(model, prompt, location), "openai request")
}

type openAIResponsesClient struct {
	client *openai.Client
	model  string
}

func newOpenAIResponsesClient(cfg OpenAIConfig, modelError string) (openAIResponsesClient, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return openAIResponsesClient{}, errors.New("openai api key is required")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return openAIResponsesClient{}, errors.New(modelError)
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(openAIHTTPClient{base: httpClient}),
		// Temporal owns activity retries. An SDK retry here would multiply calls
		// and make the activity-level retry policy inaccurate.
		option.WithMaxRetries(0),
	)
	return openAIResponsesClient{client: &client, model: model}, nil
}

func responsesParams(model, prompt string, location Location) responses.ResponseNewParams {
	return responses.ResponseNewParams{
		Model: shared.ResponsesModel(model),
		Input: responses.ResponseNewParamsInputUnion{OfString: openai.String(prompt)},
		Store: openai.Bool(false),
		Tools: []responses.ToolUnionParam{webSearchTool(location)},
	}
}

func webSearchTool(location Location) responses.ToolUnionParam {
	tool := responses.ToolParamOfWebSearch(responses.WebSearchToolTypeWebSearch)
	tool.OfWebSearch.SearchContextSize = responses.WebSearchToolSearchContextSizeMedium
	tool.OfWebSearch.UserLocation = responses.WebSearchToolUserLocationParam{
		Type:     openAIApproximateLocationType,
		Country:  optionalString(location.Country),
		City:     optionalString(location.City),
		Region:   optionalString(location.Region),
		Timezone: optionalString(location.Timezone),
	}
	return tool
}

func optionalString(value string) (out param.Opt[string]) {
	if value != "" {
		out = openai.String(value)
	}
	return out
}

func strictJSONSchemaFormat(name string, schema json.RawMessage) responses.ResponseFormatTextConfigUnionParam {
	var schemaObject map[string]any
	if err := json.Unmarshal(schema, &schemaObject); err != nil {
		panic("invalid JSON schema: " + err.Error())
	}
	format := responses.ResponseFormatTextConfigParamOfJSONSchema(name, schemaObject)
	format.OfJSONSchema.Strict = openai.Bool(true)
	return format
}

func marshalOpenAIRequest(params responses.ResponseNewParams, label string) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", label, err)
	}
	return raw, nil
}

func (c openAIResponsesClient) call(ctx context.Context, params responses.ResponseNewParams) (openAIParsedResponse, json.RawMessage, error) {
	capture := &openAIResponseCapture{}
	_, sdkErr := c.client.Responses.New(context.WithValue(ctx, openAIResponseCaptureKey{}, capture), params)
	if capture.tooLarge {
		return openAIParsedResponse{}, nil, errors.New("openai response body exceeds size limit")
	}
	if capture.readErr != nil {
		return openAIParsedResponse{}, nil, fmt.Errorf("read openai response: %w", capture.readErr)
	}
	if capture.statusCode != 0 {
		if capture.statusCode < 200 || capture.statusCode >= 300 {
			return openAIParsedResponse{}, nil, openAIHTTPError(capture.statusCode, capture.body)
		}
		body := append(json.RawMessage(nil), capture.body...)
		parsed, err := parseOpenAIResponse(body)
		return parsed, body, err
	}
	if sdkErr != nil {
		return openAIParsedResponse{}, nil, classifyOpenAISDKError(sdkErr)
	}
	return openAIParsedResponse{}, nil, errors.New("openai response is missing captured HTTP response")
}

type openAIResponseCaptureKey struct{}

type openAIResponseCapture struct {
	statusCode int
	body       []byte
	readErr    error
	tooLarge   bool
}

// openAIHTTPClient reads and caps the provider response before the SDK decoder
// sees it, then replaces the body so the SDK can continue normally. Capture
// state is request-scoped through the context, so one shared SDK client remains
// safe for concurrent activity calls.
type openAIHTTPClient struct {
	base *http.Client
}

func (c openAIHTTPClient) Do(req *http.Request) (*http.Response, error) {
	response, err := c.base.Do(req)
	if err != nil {
		return nil, err
	}

	body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(maxOpenAIResponseBodyBytes)+1))
	_ = response.Body.Close()

	capture, _ := req.Context().Value(openAIResponseCaptureKey{}).(*openAIResponseCapture)
	if capture != nil {
		capture.statusCode = response.StatusCode
		capture.readErr = readErr
		capture.tooLarge = len(body) > maxOpenAIResponseBodyBytes
		if !capture.tooLarge {
			capture.body = append([]byte(nil), body...)
		}
	}
	if readErr != nil {
		return nil, readErr
	}

	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func classifyOpenAISDKError(err error) error {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("call openai responses: %w", err)
	}
	body := json.RawMessage(apiErr.RawJSON())
	if len(body) > 0 {
		body = json.RawMessage(`{"error":` + string(body) + `}`)
	}
	return &RunnerError{
		StatusCode:   apiErr.StatusCode,
		Type:         apiErr.Type,
		Code:         apiErr.Code,
		Message:      apiErr.Message,
		Body:         append(json.RawMessage(nil), body...),
		nonRetryable: apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && apiErr.StatusCode != http.StatusTooManyRequests,
	}
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

func validateOpenAIResponse(parsed openAIParsedResponse, body []byte, missingModel, missingText string) error {
	if parsed.Status != "completed" {
		return incompleteOpenAIResponseError(parsed, body)
	}
	if parsed.Refusal != "" {
		return &RunnerError{
			Type:         "content_policy_refusal",
			Message:      parsed.Refusal,
			Body:         append(json.RawMessage(nil), body...),
			nonRetryable: true,
		}
	}
	if strings.TrimSpace(parsed.Model) == "" {
		return errors.New(missingModel)
	}
	if strings.TrimSpace(parsed.Text) == "" {
		return errors.New(missingText)
	}
	return nil
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
