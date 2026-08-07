package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIQuestionsBuildsStructuredRequest(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != openAIResponsesPath {
			t.Errorf("path = %s, want %s", r.URL.Path, openAIResponsesPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"model": "gpt-5-mini-2026",
			"output": [{"type":"message","content":[{"type":"output_text","text":"{\"prompts\":[{\"text\":\"best orthopaedic clinic in Singapore\"}]}"}]}]
		}`))
	}))
	defer server.Close()

	runner, err := NewOpenAIQuestionsRunner(OpenAIConfig{APIKey: "sk-test", Model: "gpt-5-mini", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewOpenAIQuestionsRunner: %v", err)
	}

	res, err := runner.RunQuestions(context.Background(), QuestionsInput{
		Category:    "orthopaedic clinic",
		Services:    []string{"ACL reconstruction"},
		City:        "Singapore",
		PromptLimit: 1,
	})
	if err != nil {
		t.Fatalf("RunQuestions: %v", err)
	}
	if res.Model != "gpt-5-mini-2026" {
		t.Errorf("Model = %q, want reported model", res.Model)
	}

	// This is a cheap structured-generation call, not research: no web_search
	// tool, unlike propose-profile.
	if _, ok := gotRequest["tools"]; ok {
		t.Error("questions request unexpectedly included a tools field")
	}
	if gotRequest["store"] != false {
		t.Errorf("store = %v, want false", gotRequest["store"])
	}

	format := gotRequest["text"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["strict"] != true || format["name"] != openAIQuestionsSchemaName {
		t.Errorf("format = %#v", format)
	}

	input := gotRequest["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("input turns = %d, want 2 (developer, user)", len(input))
	}
	developerContent := input[0].(map[string]any)["content"].(string)
	if developerContent != questionsInstructions {
		t.Errorf("developer instructions = %q, want %q", developerContent, questionsInstructions)
	}
	userContent := input[1].(map[string]any)["content"].(string)
	if !strings.Contains(userContent, `"prompt_count":1`) {
		t.Errorf("user content missing prompt_count: %s", userContent)
	}
	if !strings.Contains(userContent, "ACL reconstruction") {
		t.Errorf("user content missing services: %s", userContent)
	}
}

func TestOpenAIQuestionsAppendsRetryTurns(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"model": "gpt-5-mini",
			"output": [{"type":"message","content":[{"type":"output_text","text":"{\"prompts\":[]}"}]}]
		}`))
	}))
	defer server.Close()

	runner, _ := NewOpenAIQuestionsRunner(OpenAIConfig{APIKey: "sk-test", Model: "gpt-5-mini", BaseURL: server.URL})
	if _, err := runner.RunQuestions(context.Background(), QuestionsInput{
		PromptLimit:           4,
		PriorOutputJSON:       json.RawMessage(`{"prompts":[]}`),
		RetryValidationErrors: []string{"prompts has 0 entries, want exactly 4"},
	}); err != nil {
		t.Fatalf("RunQuestions: %v", err)
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

func TestQuestionsSchemaIsValidJSON(t *testing.T) {
	if !json.Valid(questionsSchema) {
		t.Fatal("questionsSchema is not valid JSON")
	}
}
