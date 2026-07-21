package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIExtractionBuildsStructuredRequest(t *testing.T) {
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
			"id": "resp_x",
			"status": "completed",
			"model": "gpt-mini-2026",
			"output": [{
				"type": "message",
				"content": [{"type": "output_text", "text": "{\"entities\":[],\"target\":null,\"citations\":[]}"}]
			}]
		}`))
	}))
	defer server.Close()

	runner, err := NewOpenAIExtractionRunner(OpenAIConfig{
		APIKey:  "sk-test",
		Model:   "gpt-mini",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAIExtractionRunner: %v", err)
	}

	res, err := runner.RunExtraction(context.Background(), ExtractionInput{
		ResponseText:     "Visit Example Clinic for a checkup.",
		Prompt:           "best clinic?",
		BusinessName:     "Example Clinic",
		BusinessCategory: "clinic",
		BusinessLocation: "Singapore, SG",
	})
	if err != nil {
		t.Fatalf("RunExtraction: %v", err)
	}
	if res.Model != "gpt-mini-2026" {
		t.Errorf("Model = %q, want reported model", res.Model)
	}
	if string(res.RawJSON) != `{"entities":[],"target":null,"citations":[]}` {
		t.Errorf("RawJSON = %q", res.RawJSON)
	}

	// This is a plain structured-JSON call, not a web-search call: no tools field.
	if _, ok := gotRequest["tools"]; ok {
		t.Error("extraction request unexpectedly included a tools field")
	}
	if gotRequest["store"] != false {
		t.Errorf("store = %v, want false", gotRequest["store"])
	}

	text, ok := gotRequest["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %#v", gotRequest["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("text.format = %#v", text["format"])
	}
	if format["type"] != "json_schema" {
		t.Errorf("text.format.type = %v, want json_schema", format["type"])
	}
	if format["strict"] != true {
		t.Errorf("text.format.strict = %v, want true", format["strict"])
	}
	if format["name"] != openAIExtractionSchemaName {
		t.Errorf("text.format.name = %v, want %q", format["name"], openAIExtractionSchemaName)
	}
	if _, ok := format["schema"].(map[string]any); !ok {
		t.Errorf("text.format.schema is not an object: %#v", format["schema"])
	}
}

func TestOpenAIExtractionAppendsRetryTurns(t *testing.T) {
	var gotRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"model": "gpt-mini-2026",
			"output": [{"type":"message","content":[{"type":"output_text","text":"{\"entities\":[],\"target\":null,\"citations\":[]}"}]}]
		}`))
	}))
	defer server.Close()

	runner, err := NewOpenAIExtractionRunner(OpenAIConfig{APIKey: "sk-test", Model: "gpt-mini", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewOpenAIExtractionRunner: %v", err)
	}

	if _, err := runner.RunExtraction(context.Background(), ExtractionInput{
		ResponseText:          "Visit Example Clinic.",
		PriorOutputJSON:       json.RawMessage(`{"entities":[{"verbatim_name":"Fabricated","is_target":false,"excerpt":"nope"}],"target":null,"citations":[]}`),
		RetryValidationErrors: []string{`entity[0].verbatim_name "Fabricated" does not appear verbatim in the response text`},
	}); err != nil {
		t.Fatalf("RunExtraction: %v", err)
	}

	input, ok := gotRequest["input"].([]any)
	if !ok {
		t.Fatalf("input = %#v", gotRequest["input"])
	}
	// developer, user, assistant (prior output), user (validation errors).
	if len(input) != 4 {
		t.Fatalf("input turns = %d, want 4 on retry", len(input))
	}
	roles := make([]string, len(input))
	for i, turn := range input {
		roles[i] = turn.(map[string]any)["role"].(string)
	}
	want := []string{"developer", "user", "assistant", "user"}
	for i := range want {
		if roles[i] != want[i] {
			t.Errorf("input[%d].role = %q, want %q", i, roles[i], want[i])
		}
	}
}

func TestExtractionSchemaIsValidJSON(t *testing.T) {
	if !json.Valid(extractionSchema) {
		t.Fatal("extractionSchema is not valid JSON")
	}
}
