package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"opensight/internal/llm"

	"go.temporal.io/sdk/temporal"
)

// proposeRunner is a ProposeProfileRunner test double returning a canned result
// or error, and recording whether it was called.
type proposeRunner struct {
	result llm.ProposeProfileRunResult
	err    error
	called bool
}

func (r *proposeRunner) RunProposeProfile(_ context.Context, _ llm.ProposeProfileInput) (llm.ProposeProfileRunResult, error) {
	r.called = true
	return r.result, r.err
}

// validProposalJSON is a well-formed 4-prompt proposal the happy path decodes.
func validProposalJSON(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(llm.ProposalPayload{
		Profile: llm.ProposedProfile{
			Name:     "Novena Ortho Clinic",
			Category: "orthopaedic clinic",
			Location: llm.ProposedLocation{Country: "SG"},
		},
		Prompts: []llm.ProposedPrompt{
			{Text: "best orthopaedic clinic in Singapore", Kind: "category"},
			{Text: "where to get ACL reconstruction", Kind: "service"},
			{Text: "knee pain who to see", Kind: "condition"},
			{Text: "orthopaedic specialist near Novena", Kind: "location"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestProposeProfileBadInputNonRetryable(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   ProposeProfileInput
	}{
		{"empty name", ProposeProfileInput{Name: "  ", PromptLimit: 4}},
		{"non-positive prompt limit", ProposeProfileInput{Name: "Clinic", PromptLimit: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &proposeRunner{}
			a := &Activities{Proposer: runner}
			_, err := a.ProposeProfile(context.Background(), tc.in)
			var appErr *temporal.ApplicationError
			if !errors.As(err, &appErr) || !appErr.NonRetryable() {
				t.Fatalf("want non-retryable ApplicationError, got %T: %v", err, err)
			}
			if runner.called {
				t.Fatal("runner should not be called for bad input")
			}
		})
	}
}

func TestProposeProfileRunnerErrorPropagates(t *testing.T) {
	// A transient runner error propagates unchanged for Temporal's default retry.
	transient := errors.New("openai: status 503")
	a := &Activities{Proposer: &proposeRunner{err: transient}}
	_, err := a.ProposeProfile(context.Background(), ProposeProfileInput{Name: "Clinic", PromptLimit: 4})
	if !errors.Is(err, transient) {
		t.Fatalf("want transient error propagated, got %v", err)
	}
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.NonRetryable() {
		t.Fatal("transient error must not be wrapped non-retryable")
	}

	// A runner error marked non-retryable becomes a non-retryable activity error.
	refused := errors.Join(errors.New("refused"), llm.ErrNonRetryable)
	a = &Activities{Proposer: &proposeRunner{err: refused}}
	_, err = a.ProposeProfile(context.Background(), ProposeProfileInput{Name: "Clinic", PromptLimit: 4})
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("want non-retryable ApplicationError for refusal, got %T: %v", err, err)
	}
}

func TestProposeProfileHappyPath(t *testing.T) {
	runner := &proposeRunner{result: llm.ProposeProfileRunResult{RawJSON: validProposalJSON(t), Model: "gpt-x"}}
	a := &Activities{Proposer: runner}
	out, err := a.ProposeProfile(context.Background(), ProposeProfileInput{Name: "Novena Ortho Clinic", PromptLimit: 4})
	if err != nil {
		t.Fatalf("ProposeProfile: %v", err)
	}
	if !out.Proposed {
		t.Fatalf("want Proposed, got %+v", out)
	}
	if out.Model != "gpt-x" {
		t.Fatalf("Model = %q, want gpt-x", out.Model)
	}
	if out.Payload.Profile.Category != "orthopaedic clinic" || len(out.Payload.Prompts) != 4 {
		t.Fatalf("payload not passed through: %+v", out.Payload)
	}
}
