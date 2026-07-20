package llm

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
)

// stubResponse is a canned, deterministic clinic-recommendation payload (with
// url_citation annotations) returned for every prompt in stub mode (design 07
// "Local development"). It never touches the network.
//
//go:embed testdata/stub/response.json
var stubResponse []byte

// StubPromptRunner returns the same canned response for any prompt. It exists so
// development and tests are fast, offline, and deterministic without depending
// on a recorded fixture matching the prompt.
type StubPromptRunner struct {
	raw json.RawMessage
}

// NewStubPromptRunner validates the embedded canned payload once at startup.
func NewStubPromptRunner() (*StubPromptRunner, error) {
	if !json.Valid(stubResponse) {
		return nil, errors.New("stub response fixture is not valid JSON")
	}
	return &StubPromptRunner{raw: append(json.RawMessage(nil), stubResponse...)}, nil
}

// RunPrompt returns the canned response, with the request body reflecting the
// caller's actual prompt and location.
func (r *StubPromptRunner) RunPrompt(_ context.Context, req PromptRequest) (PromptRunResult, error) {
	if r == nil {
		return PromptRunResult{}, errors.New("stub prompt runner is nil")
	}
	return resultFromRawResponse(req.Prompt, req.Location, r.raw)
}
