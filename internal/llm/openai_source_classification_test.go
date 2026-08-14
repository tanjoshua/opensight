package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAISourceClassificationCarriesQuestionAndOpportunitySchema(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"completed","model":"gpt-5.6-terra-2026-08-01",
			"output":[{"type":"message","content":[{"type":"output_text","text":"{\"sources\":[{\"candidate_index\":0,\"classification\":\"competitor_owned\",\"owner\":\"Rival Clinic\",\"claim_indices\":[0]}],\"content_opportunities\":[]}"}]}]
		}`))
	}))
	defer server.Close()

	runner, err := NewOpenAISourceClassifier(OpenAIConfig{APIKey: "sk-test", Model: "gpt-5.6-terra", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.ClassifySources(context.Background(), SourceClassificationInput{
		BusinessName: "Customer Clinic", SiteContent: "Customer site text.",
		Candidates: []SourceCandidate{{Domain: "rival.example", Claims: []SourceClaim{{
			Owner: "Rival Clinic", Passage: "Rival Clinic publishes evening availability.",
			Question: "Which clinics are open after work?", ResultID: "result-1", PromptID: "prompt-1",
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	input := request["input"].([]any)
	developer := input[0].(map[string]any)["content"].(string)
	user := input[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "Which clinics are open after work?") {
		t.Fatalf("monitored question missing from classifier context: %s", user)
	}
	if strings.Contains(developer, "materially helped competitors appear") || !strings.Contains(developer, "not established causes") {
		t.Fatalf("classifier prompt does not preserve the non-causal contract: %s", developer)
	}
	format := request["text"].(map[string]any)["format"].(map[string]any)
	schema, err := json.Marshal(format["schema"])
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"content_opportunities", "observation", "site_state", "suggested_action", "supported_point"} {
		if !strings.Contains(string(schema), field) {
			t.Errorf("structured schema missing %q: %s", field, schema)
		}
	}
}
