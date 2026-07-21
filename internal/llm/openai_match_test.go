package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIMatchBuildsStructuredRequest(t *testing.T) {
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
			"model": "gpt-mini-2026",
			"output": [{"type":"message","content":[{"type":"output_text","text":"{\"matches\":[{\"index\":0,\"competitor_id\":null}]}"}]}]
		}`))
	}))
	defer server.Close()

	runner, err := NewOpenAIMatchRunner(OpenAIConfig{APIKey: "sk-test", Model: "gpt-mini", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewOpenAIMatchRunner: %v", err)
	}

	cid := mustID(t)
	res, err := runner.RunMatch(context.Background(), MatchInput{
		Names:       []string{"Atlas Dental Clinic"},
		Competitors: []MatchCandidate{{ID: cid, Name: "Atlas Dental", Aliases: []string{"atlas"}, Website: "atlas.example.com"}},
	})
	if err != nil {
		t.Fatalf("RunMatch: %v", err)
	}
	if res.Model != "gpt-mini-2026" {
		t.Errorf("Model = %q, want reported model", res.Model)
	}

	// Structured-JSON call, not a web-search call: no tools field.
	if _, ok := gotRequest["tools"]; ok {
		t.Error("match request unexpectedly included a tools field")
	}
	if gotRequest["store"] != false {
		t.Errorf("store = %v, want false", gotRequest["store"])
	}

	format := gotRequest["text"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Errorf("text.format.type = %v, want json_schema", format["type"])
	}
	if format["strict"] != true {
		t.Errorf("text.format.strict = %v, want true", format["strict"])
	}
	if format["name"] != openAIMatchSchemaName {
		t.Errorf("text.format.name = %v, want %q", format["name"], openAIMatchSchemaName)
	}

	// The competitor id must reach the model so it can echo a match back.
	input := gotRequest["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("input turns = %d, want 2 (developer, user)", len(input))
	}
	userContent := input[1].(map[string]any)["content"].(string)
	if !strings.Contains(userContent, cid.String()) {
		t.Errorf("user content does not carry competitor id %q: %s", cid.String(), userContent)
	}
}

func TestMatchSchemaIsValidJSON(t *testing.T) {
	if !json.Valid(matchSchema) {
		t.Fatal("matchSchema is not valid JSON")
	}
}
