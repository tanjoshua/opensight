package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// NewPromptRunner selects a PromptRunner implementation by mode (07 "Local
// development"): "stub" and "replay" spend no OpenAI money, "openai" is the real
// Responses runner. openAICfg is only consulted for the "openai" mode.
func NewPromptRunner(mode string, openAICfg OpenAIConfig) (PromptRunner, error) {
	switch mode {
	case "stub":
		return NewStubPromptRunner()
	case "replay":
		return NewReplayPromptRunner()
	case "openai":
		return NewOpenAIPromptRunner(openAICfg)
	default:
		return nil, fmt.Errorf("unknown prompt runner mode %q", mode)
	}
}

// resultFromRawResponse turns a recorded (replay) or canned (stub) OpenAI
// Responses payload into a PromptRunResult, applying the same completion,
// refusal, and required-field checks as the live runner so every mode produces
// identically shaped, persistable output. The request body is rebuilt from the
// prompt and location using the payload's own reported model.
func resultFromRawResponse(prompt string, location Location, raw json.RawMessage) (PromptRunResult, error) {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return PromptRunResult{}, errors.New("prompt is required")
	}
	loc := location.normalized()
	if err := loc.validate(); err != nil {
		return PromptRunResult{}, err
	}

	parsed, err := parseOpenAIResponse(raw)
	if err != nil {
		// A hard parse failure means a broken fixture (dev-environment bug), not
		// a modeled runtime failure — nothing worth persisting a request for.
		return PromptRunResult{}, err
	}

	// Build the request body from the parsed model as soon as the payload parses,
	// so every modeled-failure early return below still carries RequestJSON for
	// ExecutePrompt (RUN-4) to persist a failed row.
	requestJSON, err := buildResponsesRequestJSON(parsed.Model, p, loc)
	if err != nil {
		return PromptRunResult{}, err
	}

	if parsed.Status != "completed" {
		return PromptRunResult{RequestJSON: requestJSON}, incompleteOpenAIResponseError(parsed, raw)
	}
	if parsed.Refusal != "" {
		return PromptRunResult{RequestJSON: requestJSON}, &RunnerError{
			Type:         "content_policy_refusal",
			Message:      parsed.Refusal,
			Body:         append(json.RawMessage(nil), raw...),
			nonRetryable: true,
		}
	}
	if strings.TrimSpace(parsed.Model) == "" {
		return PromptRunResult{RequestJSON: requestJSON}, errors.New("response missing reported model")
	}
	if strings.TrimSpace(parsed.Text) == "" {
		return PromptRunResult{RequestJSON: requestJSON}, errors.New("response missing output_text")
	}

	return PromptRunResult{
		RequestJSON:  requestJSON,
		RawResponse:  append(json.RawMessage(nil), raw...),
		ResponseText: parsed.Text,
		Model:        strings.TrimSpace(parsed.Model),
	}, nil
}
