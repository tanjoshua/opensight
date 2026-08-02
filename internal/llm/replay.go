package llm

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// replayFS holds the recorded OpenAI raw_response payloads that back replay
// mode; index.json maps prompt text to a payload file.
//
//go:embed testdata/replay/index.json testdata/replay/*.json
var replayFS embed.FS

const replayIndexPath = "testdata/replay/index.json"

// ErrNoReplayFixture is returned when replay mode is asked for a prompt that has
// no recorded payload. It is a configuration error, not a transient one.
var ErrNoReplayFixture = errors.New("no replay fixture for prompt")

type replayIndexEntry struct {
	Prompt string `json:"prompt"`
	File   string `json:"file"`
}

// ReplayPromptRunner replays recorded real OpenAI responses keyed by prompt
// text, so development and tests get real answers at zero cost and full
// determinism (design 07 "Local development").
type ReplayPromptRunner struct {
	responses map[string]json.RawMessage
}

// NewReplayPromptRunner loads the embedded replay corpus.
func NewReplayPromptRunner() (*ReplayPromptRunner, error) {
	indexRaw, err := replayFS.ReadFile(replayIndexPath)
	if err != nil {
		return nil, fmt.Errorf("read replay index: %w", err)
	}
	var entries []replayIndexEntry
	if err := json.Unmarshal(indexRaw, &entries); err != nil {
		return nil, fmt.Errorf("parse replay index: %w", err)
	}

	responses := make(map[string]json.RawMessage, len(entries))
	for _, entry := range entries {
		key := replayKey(entry.Prompt)
		if key == "" {
			return nil, errors.New("replay index entry is missing a prompt")
		}
		if _, exists := responses[key]; exists {
			return nil, fmt.Errorf("replay index has duplicate prompt %q", entry.Prompt)
		}
		raw, err := replayFS.ReadFile("testdata/replay/" + entry.File)
		if err != nil {
			return nil, fmt.Errorf("read replay fixture %q: %w", entry.File, err)
		}
		responses[key] = raw
	}
	if len(responses) == 0 {
		return nil, errors.New("replay corpus is empty")
	}
	return &ReplayPromptRunner{responses: responses}, nil
}

// RunPrompt returns the recorded response for req.Prompt. A prompt without a
// fixture is a hard error (ErrNoReplayFixture): replay mode is only meaningful
// for prompts in the corpus.
func (r *ReplayPromptRunner) RunPrompt(_ context.Context, req PromptRequest) (PromptRunResult, error) {
	if r == nil {
		return PromptRunResult{}, errors.New("replay prompt runner is nil")
	}
	raw, ok := r.responses[replayKey(req.Prompt)]
	if !ok {
		return PromptRunResult{}, fmt.Errorf("%w: %q", ErrNoReplayFixture, strings.TrimSpace(req.Prompt))
	}
	return resultFromRawResponse(req.Prompt, req.Location, raw)
}

// replayKey normalizes prompt text so trivial whitespace differences still hit
// the same fixture.
func replayKey(prompt string) string {
	return strings.Join(strings.Fields(prompt), " ")
}
