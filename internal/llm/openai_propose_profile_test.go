package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const proposalResponseText = `{"low_confidence":false,"profile":{"name":"Clinic","aliases":[],"category":"clinic","services":[],"location":{"address":"","area":"","city":"","country":"SG"}},"prompts":[]}`

func proposalCompletedBody(t *testing.T) string {
	t.Helper()
	inner, err := json.Marshal(proposalResponseText)
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	return `{"status":"completed","model":"gpt-mini-2026","output":[{"type":"message","content":[{"type":"output_text","text":` + string(inner) + `}]}]}`
}

func TestOpenAIProposeProfileBuildsStructuredRequest(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != openAIResponsesPath {
			t.Errorf("path = %s, want %s", r.URL.Path, openAIResponsesPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(proposalCompletedBody(t)))
	}))
	defer server.Close()

	runner, err := NewOpenAIProposeProfileRunner(OpenAIConfig{APIKey: "sk-test", Model: "gpt-mini", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewOpenAIProposeProfileRunner: %v", err)
	}

	res, err := runner.RunProposeProfile(context.Background(), ProposeProfileInput{
		Name:        "Clinic",
		SiteText:    "some site text",
		Location:    Location{Country: "SG"},
		PromptLimit: 12,
	})
	if err != nil {
		t.Fatalf("RunProposeProfile: %v", err)
	}
	if res.Model != "gpt-mini-2026" {
		t.Errorf("Model = %q, want reported model", res.Model)
	}
	if string(res.RawJSON) != proposalResponseText {
		t.Errorf("RawJSON = %q", res.RawJSON)
	}

	if gotRequest["store"] != false {
		t.Errorf("store = %v, want false", gotRequest["store"])
	}

	// The combined research+draft call attaches the web_search tool with a forced
	// tool_choice AND the strict JSON schema — the spike confirmed these coexist.
	tools, ok := gotRequest["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one web_search tool", gotRequest["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != openAIWebSearchToolType {
		t.Errorf("tool type = %v, want %q", tool["type"], openAIWebSearchToolType)
	}
	if loc := tool["user_location"].(map[string]any); loc["country"] != "SG" {
		t.Errorf("user_location.country = %v, want SG", loc["country"])
	}
	if gotRequest["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want required", gotRequest["tool_choice"])
	}

	format := gotRequest["text"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["strict"] != true || format["name"] != openAIProposeProfileSchemaName {
		t.Errorf("format = %#v", format)
	}

	// The user turn carries the prompt count so the model never hardcodes one.
	input := gotRequest["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("input turns = %d, want 2 (developer, user)", len(input))
	}
	var userContent map[string]any
	if err := json.Unmarshal([]byte(input[1].(map[string]any)["content"].(string)), &userContent); err != nil {
		t.Fatalf("decode user content: %v", err)
	}
	if userContent["prompt_count"] != float64(12) {
		t.Errorf("prompt_count = %v, want 12", userContent["prompt_count"])
	}
}

func TestOpenAIProposeProfileAppendsRetryTurns(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(proposalCompletedBody(t)))
	}))
	defer server.Close()

	runner, _ := NewOpenAIProposeProfileRunner(OpenAIConfig{APIKey: "sk-test", Model: "gpt-mini", BaseURL: server.URL})
	if _, err := runner.RunProposeProfile(context.Background(), ProposeProfileInput{
		Name:                  "Clinic",
		PromptLimit:           4,
		PriorOutputJSON:       json.RawMessage(`{"prompts":[]}`),
		RetryValidationErrors: []string{"prompts has 0 entries, want exactly 4"},
	}); err != nil {
		t.Fatalf("RunProposeProfile: %v", err)
	}

	input := gotRequest["input"].([]any)
	roles := make([]string, len(input))
	for i, turn := range input {
		roles[i] = turn.(map[string]any)["role"].(string)
	}
	want := []string{"developer", "user", "assistant", "user"}
	if len(roles) != len(want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Errorf("input[%d].role = %q, want %q", i, roles[i], want[i])
		}
	}
}

func TestOpenAIProposeProfileErrorMapping(t *testing.T) {
	tests := []struct {
		name            string
		status          int
		body            string
		wantNonRetry    bool
		wantErrContains string
	}{
		{
			name:         "refusal is non-retryable",
			status:       200,
			body:         `{"status":"completed","model":"gpt-mini","output":[{"type":"message","content":[{"type":"refusal","refusal":"cannot help"}]}]}`,
			wantNonRetry: true,
		},
		{
			name:            "incomplete status errors",
			status:          200,
			body:            `{"status":"incomplete","model":"gpt-mini","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
			wantErrContains: "incomplete",
		},
		{
			name:         "400 is non-retryable",
			status:       400,
			body:         `{"error":{"message":"bad request","type":"invalid_request_error"}}`,
			wantNonRetry: true,
		},
		{
			name:         "503 is retryable",
			status:       503,
			body:         `{"error":{"message":"unavailable"}}`,
			wantNonRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			runner, _ := NewOpenAIProposeProfileRunner(OpenAIConfig{APIKey: "sk-test", Model: "gpt-mini", BaseURL: server.URL})
			_, err := runner.RunProposeProfile(context.Background(), ProposeProfileInput{Name: "Clinic", PromptLimit: 4})
			if err == nil {
				t.Fatal("want error")
			}
			if got := errors.Is(err, ErrNonRetryable); got != tt.wantNonRetry {
				t.Fatalf("non-retryable = %v, want %v (err: %v)", got, tt.wantNonRetry, err)
			}
			if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
				t.Fatalf("err %q does not contain %q", err.Error(), tt.wantErrContains)
			}
		})
	}
}

func TestProposeProfileSchemaIsValidJSON(t *testing.T) {
	if !json.Valid(proposeProfileSchema) {
		t.Fatal("proposeProfileSchema is not valid JSON")
	}
}
