package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"opensight/internal/domain"
)

// MatchCandidate is one candidate the LLM match pass judges an unmatched name
// against (design 05 step 3) — either the target business or an existing
// competitor. Name + aliases + website are the only evidence the model is
// allowed to use; ID is echoed back on a match.
type MatchCandidate struct {
	ID      domain.ID
	Name    string
	Aliases []string
	Website string
}

// MatchInput is one whole-run LLM match call: the run's still-unmatched verbatim
// names (deduped by normalized key upstream) against the full candidate list.
//
// Target is the run's own business, and is a candidate in its own right: the
// exact pass only self-matches on the business name and its approved aliases, so
// without it every unrecognised variant of the user's own name would fall
// through to "new discovered competitor".
type MatchInput struct {
	Names       []string
	Target      MatchCandidate
	Competitors []MatchCandidate
}

// MatchRunResult is the raw output of one match call, left unmarshalled for
// DecodeMatchOutput — the same runner/decoder split as extraction.
type MatchRunResult struct {
	RawJSON json.RawMessage
	Model   string
}

// MatchRunner runs one conservative cheap-model match call per run for the
// still-unmatched names (design 05 step 3).
type MatchRunner interface {
	RunMatch(ctx context.Context, in MatchInput) (MatchRunResult, error)
}

// NewMatchRunner selects a MatchRunner by mode (see the package doc). The stub
// matches nothing, so a still-unmatched name always degrades to a new
// discovered competitor. openAICfg is only consulted for "openai".
func NewMatchRunner(mode string, openAICfg OpenAIConfig) (MatchRunner, error) {
	switch mode {
	case "stub", "replay":
		return NewStubMatchRunner()
	case "openai":
		return NewOpenAIMatchRunner(openAICfg)
	default:
		return nil, fmt.Errorf("unknown match runner mode %q", mode)
	}
}

// StubMatchRunner matches nothing: every name comes back null, so reconcile
// treats them all as new. This is the dev/test default so no run touches OpenAI.
type StubMatchRunner struct{}

// NewStubMatchRunner returns the offline stub matcher.
func NewStubMatchRunner() (*StubMatchRunner, error) {
	return &StubMatchRunner{}, nil
}

// RunMatch returns an all-null verdict regardless of input.
func (r *StubMatchRunner) RunMatch(_ context.Context, in MatchInput) (MatchRunResult, error) {
	matches := make([]matchEntry, len(in.Names))
	for i := range in.Names {
		matches[i] = matchEntry{Index: i, MatchID: nil}
	}
	raw, err := json.Marshal(matchOutput{Matches: matches})
	if err != nil {
		return MatchRunResult{}, err
	}
	return MatchRunResult{RawJSON: raw, Model: "stub-match"}, nil
}

// matchOutput is the decoded match schema: one entry per index into MatchInput's
// Names, match_id null for no match.
type matchOutput struct {
	Matches []matchEntry `json:"matches"`
}

type matchEntry struct {
	Index   int     `json:"index"`
	MatchID *string `json:"match_id"`
}

// DecodeMatchOutput turns a raw match verdict into one resolved id per in.Names
// index — a competitor id, the target business id, or uuid.Nil meaning "no
// match". It is deliberately forgiving: any malformed, duplicate,
// out-of-range, or unknown-id entry clamps that index to uuid.Nil and adds a
// warning rather than erroring — a garbled verdict just degrades to "unmatched
// -> new discovered", which is the conservative default (design 05: "when in
// doubt, it's new"). Indices not mentioned by the model also stay uuid.Nil.
func DecodeMatchOutput(raw json.RawMessage, in MatchInput) ([]domain.ID, []string, error) {
	matches := make([]domain.ID, len(in.Names))

	var out matchOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		// A wholly-unparseable verdict is not a hard error: every name stays
		// unmatched, which is safe.
		return matches, []string{fmt.Sprintf("match output is not valid JSON: %v", err)}, nil
	}

	valid := make(map[string]domain.ID, len(in.Competitors)+1)
	if in.Target.ID != (domain.ID{}) {
		valid[in.Target.ID.String()] = in.Target.ID
	}
	for _, c := range in.Competitors {
		valid[c.ID.String()] = c.ID
	}

	var warnings []string
	seen := make(map[int]bool, len(out.Matches))
	for _, e := range out.Matches {
		if e.Index < 0 || e.Index >= len(in.Names) {
			warnings = append(warnings, fmt.Sprintf("match entry index %d is out of range", e.Index))
			continue
		}
		if seen[e.Index] {
			warnings = append(warnings, fmt.Sprintf("match entry index %d is duplicated; ignoring the later one", e.Index))
			continue
		}
		seen[e.Index] = true
		if e.MatchID == nil {
			continue // explicit no-match
		}
		id, ok := valid[*e.MatchID]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("match entry index %d names id %q not in the candidate list; treating as unmatched", e.Index, *e.MatchID))
			continue
		}
		matches[e.Index] = id
	}

	// Unset indices keep the zero domain.ID (uuid.Nil) — "no match".
	return matches, warnings, nil
}
