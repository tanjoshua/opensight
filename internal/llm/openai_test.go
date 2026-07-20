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

func TestOpenAIRunnerBuildsResponsesRequest(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != openAIResponsesPath {
			t.Errorf("path = %s, want %s", r.URL.Path, openAIResponsesPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "resp_test",
			"status": "completed",
			"model": "gpt-real-2026-07-19",
			"output": [{
				"type": "web_search_call",
				"status": "completed"
			}, {
				"type": "message",
				"content": [{
					"type": "output_text",
					"text": "Use Example Clinic."
				}]
			}]
		}`))
	}))
	defer server.Close()

	runner, err := NewOpenAIPromptRunner(OpenAIConfig{
		APIKey:  "sk-test",
		Model:   "chat-latest",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAIPromptRunner returned error: %v", err)
	}

	result, err := runner.RunPrompt(context.Background(), PromptRequest{
		Prompt: "Where should I go for root canal treatment?",
		Location: Location{
			Country:  "SG",
			City:     "Singapore",
			Region:   "Novena",
			Timezone: "Asia/Singapore",
		},
	})
	if err != nil {
		t.Fatalf("RunPrompt returned error: %v", err)
	}

	if gotRequest["model"] != "chat-latest" {
		t.Errorf("model = %v", gotRequest["model"])
	}
	if gotRequest["input"] != "Where should I go for root canal treatment?" {
		t.Errorf("input = %v", gotRequest["input"])
	}
	if gotRequest["store"] != false {
		t.Errorf("store = %v, want false", gotRequest["store"])
	}
	if _, ok := gotRequest["instructions"]; ok {
		t.Fatal("request unexpectedly included instructions")
	}

	tools, ok := gotRequest["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", gotRequest["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != openAIWebSearchToolType {
		t.Errorf("tool type = %v", tool["type"])
	}
	if tool["search_context_size"] != openAISearchContextSize {
		t.Errorf("search_context_size = %v", tool["search_context_size"])
	}
	userLocation := tool["user_location"].(map[string]any)
	for key, want := range map[string]string{
		"type":     openAIApproximateLocationType,
		"country":  "SG",
		"city":     "Singapore",
		"region":   "Novena",
		"timezone": "Asia/Singapore",
	} {
		if got := userLocation[key]; got != want {
			t.Errorf("user_location.%s = %v, want %q", key, got, want)
		}
	}

	var persisted map[string]any
	if err := json.Unmarshal(result.RequestJSON, &persisted); err != nil {
		t.Fatalf("request json invalid: %v", err)
	}
	if persisted["store"] != false {
		t.Errorf("persisted request store = %v, want false", persisted["store"])
	}
	if result.Model != "gpt-real-2026-07-19" {
		t.Errorf("Model = %q, want reported model", result.Model)
	}
	if result.ResponseText != "Use Example Clinic." {
		t.Errorf("ResponseText = %q", result.ResponseText)
	}
	if !json.Valid(result.RawResponse) {
		t.Fatal("RawResponse is not valid JSON")
	}
}

func TestParseOpenAIResponseConcatenatesOutputText(t *testing.T) {
	parsed, err := parseOpenAIResponse([]byte(`{
		"status": "completed",
		"model": "gpt-reported",
		"output": [{
			"type": "message",
			"content": [
				{"type": "output_text", "text": "First."},
				{"type": "output_text", "text": "Second."}
			]
		}]
	}`))
	if err != nil {
		t.Fatalf("parseOpenAIResponse returned error: %v", err)
	}

	if parsed.Model != "gpt-reported" {
		t.Errorf("Model = %q", parsed.Model)
	}
	if parsed.Status != "completed" {
		t.Errorf("Status = %q", parsed.Status)
	}
	if parsed.Text != "First.\nSecond." {
		t.Errorf("Text = %q", parsed.Text)
	}
}

func TestOpenAIRunnerRejectsIncompleteResponseWithText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"status": "incomplete",
			"incomplete_details": {"reason": "max_output_tokens"},
			"model": "gpt-reported",
			"output": [{
				"type": "message",
				"content": [{"type": "output_text", "text": "Partial answer."}]
			}]
		}`))
	}))
	defer server.Close()

	runner, err := NewOpenAIPromptRunner(OpenAIConfig{
		APIKey:  "sk-test",
		Model:   "chat-latest",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAIPromptRunner returned error: %v", err)
	}

	_, err = runner.RunPrompt(context.Background(), PromptRequest{
		Prompt:   "Where should I go?",
		Location: Location{Country: "SG"},
	})
	if err == nil {
		t.Fatal("expected incomplete response error")
	}
	var runnerErr *RunnerError
	if !errors.As(err, &runnerErr) {
		t.Fatalf("error type = %T, want RunnerError", err)
	}
	if runnerErr.NonRetryable() {
		t.Fatal("max_output_tokens incomplete response should remain retryable")
	}
	if !strings.Contains(err.Error(), `response status "incomplete"`) {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestOpenAIRunnerClassifiesHTTPError(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		nonRetryable   bool
		responseBody   string
		expectedSubstr string
	}{
		{
			name:           "bad request",
			status:         http.StatusBadRequest,
			nonRetryable:   true,
			responseBody:   `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad"}}`,
			expectedSubstr: "bad request",
		},
		{
			name:           "rate limit",
			status:         http.StatusTooManyRequests,
			nonRetryable:   false,
			responseBody:   `{"error":{"message":"slow down"}}`,
			expectedSubstr: "slow down",
		},
		{
			name:           "server error",
			status:         http.StatusInternalServerError,
			nonRetryable:   false,
			responseBody:   `{"error":{"message":"try again"}}`,
			expectedSubstr: "try again",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.responseBody))
			}))
			defer server.Close()

			runner, err := NewOpenAIPromptRunner(OpenAIConfig{
				APIKey:  "sk-test",
				Model:   "chat-latest",
				BaseURL: server.URL,
			})
			if err != nil {
				t.Fatalf("NewOpenAIPromptRunner returned error: %v", err)
			}

			_, err = runner.RunPrompt(context.Background(), PromptRequest{
				Prompt:   "Where should I go?",
				Location: Location{Country: "SG"},
			})
			if err == nil {
				t.Fatal("expected error")
			}
			var runnerErr *RunnerError
			if !errors.As(err, &runnerErr) {
				t.Fatalf("error type = %T, want RunnerError", err)
			}
			if runnerErr.NonRetryable() != tc.nonRetryable {
				t.Errorf("NonRetryable = %v, want %v", runnerErr.NonRetryable(), tc.nonRetryable)
			}
			if errors.Is(err, ErrNonRetryable) != tc.nonRetryable {
				t.Errorf("errors.Is ErrNonRetryable = %v, want %v", errors.Is(err, ErrNonRetryable), tc.nonRetryable)
			}
			if !strings.Contains(err.Error(), tc.expectedSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.expectedSubstr)
			}
		})
	}
}

func TestOpenAIRunnerTreatsRefusalAsNonRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"model": "gpt-reported",
			"output": [{
				"type": "message",
				"content": [{"type": "refusal", "refusal": "I cannot help with that."}]
			}]
		}`))
	}))
	defer server.Close()

	runner, err := NewOpenAIPromptRunner(OpenAIConfig{
		APIKey:  "sk-test",
		Model:   "chat-latest",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAIPromptRunner returned error: %v", err)
	}

	_, err = runner.RunPrompt(context.Background(), PromptRequest{
		Prompt:   "Where should I go?",
		Location: Location{Country: "SG"},
	})
	if err == nil {
		t.Fatal("expected refusal error")
	}
	if !errors.Is(err, ErrNonRetryable) {
		t.Fatalf("errors.Is ErrNonRetryable = false, error = %v", err)
	}
}
