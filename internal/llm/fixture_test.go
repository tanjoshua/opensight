package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func sgLocation() Location {
	return Location{Country: "SG", City: "Singapore", Region: "Central", Timezone: "Asia/Singapore"}
}

func TestStubRunnerReturnsCannedResult(t *testing.T) {
	runner, err := NewStubPromptRunner()
	if err != nil {
		t.Fatalf("NewStubPromptRunner: %v", err)
	}

	result, err := runner.RunPrompt(context.Background(), PromptRequest{
		Prompt:   "Anything at all",
		Location: sgLocation(),
	})
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Model != "stub-responses-1" {
		t.Errorf("Model = %q, want stub-responses-1", result.Model)
	}
	if strings.TrimSpace(result.ResponseText) == "" {
		t.Error("ResponseText is empty")
	}
	if !json.Valid(result.RawResponse) {
		t.Error("RawResponse is not valid JSON")
	}
	// The request must reflect the caller's actual prompt (not the canned one).
	var req map[string]any
	if err := json.Unmarshal(result.RequestJSON, &req); err != nil {
		t.Fatalf("request json invalid: %v", err)
	}
	if req["input"] != "Anything at all" {
		t.Errorf("request input = %v, want caller prompt", req["input"])
	}
	if req["store"] != false {
		t.Errorf("request store = %v, want false", req["store"])
	}
}

func TestReplayRunnerReplaysRecordedResponse(t *testing.T) {
	runner, err := NewReplayPromptRunner()
	if err != nil {
		t.Fatalf("NewReplayPromptRunner: %v", err)
	}

	// Whitespace differences must still resolve to the same fixture.
	result, err := runner.RunPrompt(context.Background(), PromptRequest{
		Prompt:   "  What are the best dental clinics in Singapore?  ",
		Location: sgLocation(),
	})
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Model != "gpt-5-2025-08-07" {
		t.Errorf("Model = %q, want the recorded model", result.Model)
	}
	if strings.TrimSpace(result.ResponseText) == "" {
		t.Error("ResponseText is empty")
	}
	if !json.Valid(result.RawResponse) {
		t.Error("RawResponse is not valid JSON")
	}
	// Determinism: a second call returns the identical recorded payload.
	again, err := runner.RunPrompt(context.Background(), PromptRequest{
		Prompt:   "What are the best dental clinics in Singapore?",
		Location: sgLocation(),
	})
	if err != nil {
		t.Fatalf("RunPrompt (again): %v", err)
	}
	if string(again.RawResponse) != string(result.RawResponse) {
		t.Error("replay is not deterministic")
	}
}

func TestReplayRunnerUnknownPromptErrors(t *testing.T) {
	runner, err := NewReplayPromptRunner()
	if err != nil {
		t.Fatalf("NewReplayPromptRunner: %v", err)
	}
	_, err = runner.RunPrompt(context.Background(), PromptRequest{
		Prompt:   "A prompt with no recorded fixture",
		Location: sgLocation(),
	})
	if !errors.Is(err, ErrNoReplayFixture) {
		t.Fatalf("error = %v, want ErrNoReplayFixture", err)
	}
}

func TestNewPromptRunnerSelectsMode(t *testing.T) {
	for _, mode := range []string{"stub", "replay"} {
		runner, err := NewPromptRunner(mode, OpenAIConfig{})
		if err != nil {
			t.Fatalf("NewPromptRunner(%q): %v", mode, err)
		}
		if runner == nil {
			t.Fatalf("NewPromptRunner(%q) returned nil runner", mode)
		}
	}
	if _, err := NewPromptRunner("bogus", OpenAIConfig{}); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
