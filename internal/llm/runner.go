package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// PromptRunner executes a single consumer-style prompt and returns the raw
// material needed to persist a reproducible prompt_result.
type PromptRunner interface {
	RunPrompt(ctx context.Context, req PromptRequest) (PromptRunResult, error)
}

// PromptRequest is one prompt execution request.
type PromptRequest struct {
	Prompt   string
	Location Location
}

// Location is the approximate user location passed to OpenAI web search. It is
// derived from businesses.location; Country must be a two-letter ISO code.
type Location struct {
	Country  string
	City     string
	Region   string
	Timezone string
}

// PromptRunResult is the successful output of a prompt execution.
type PromptRunResult struct {
	RequestJSON  json.RawMessage
	RawResponse  json.RawMessage
	ResponseText string
	Model        string
}

// ErrNonRetryable marks a runner error that RUN-4 should not retry.
var ErrNonRetryable = errors.New("non-retryable prompt runner error")

// RunnerError carries provider failure details without losing retry posture.
type RunnerError struct {
	StatusCode   int
	Type         string
	Code         string
	Message      string
	Body         json.RawMessage
	nonRetryable bool
}

func (e *RunnerError) Error() string {
	if e == nil {
		return ""
	}
	if e.StatusCode > 0 && e.Message != "" {
		return fmt.Sprintf("openai: status %d: %s", e.StatusCode, e.Message)
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("openai: status %d", e.StatusCode)
	}
	if e.Message != "" {
		return "openai: " + e.Message
	}
	return "openai: prompt runner error"
}

// Is allows errors.Is(err, ErrNonRetryable).
func (e *RunnerError) Is(target error) bool {
	return target == ErrNonRetryable && e != nil && e.nonRetryable
}

// NonRetryable reports whether a caller should skip provider retry.
func (e *RunnerError) NonRetryable() bool {
	return e != nil && e.nonRetryable
}
