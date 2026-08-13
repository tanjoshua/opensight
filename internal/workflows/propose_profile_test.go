package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"opensight/internal/llm"
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
			{Text: "best orthopaedic clinic in Singapore"},
			{Text: "where to get ACL reconstruction"},
			{Text: "knee pain who to see"},
			{Text: "orthopaedic specialist near Novena"},
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
		{"empty name", ProposeProfileInput{Name: "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &proposeRunner{}
			a := &Operations{Proposer: runner}
			_, err := a.ProposeProfile(context.Background(), tc.in)
			var appErr *PermanentError
			if !errors.As(err, &appErr) {
				t.Fatalf("want PermanentError, got %T: %v", err, err)
			}
			if runner.called {
				t.Fatal("runner should not be called for bad input")
			}
		})
	}
}

func TestProposeProfileRunnerErrorPropagates(t *testing.T) {
	// A transient runner error propagates unchanged for River's default retry.
	transient := errors.New("openai: status 503")
	a := &Operations{Proposer: &proposeRunner{err: transient}}
	_, err := a.ProposeProfile(context.Background(), ProposeProfileInput{Name: "Clinic"})
	if !errors.Is(err, transient) {
		t.Fatalf("want transient error propagated, got %v", err)
	}
	var appErr *PermanentError
	if errors.As(err, &appErr) {
		t.Fatal("transient error must not be wrapped non-retryable")
	}

	// A runner error marked non-retryable becomes a non-retryable operation error.
	refused := errors.Join(errors.New("refused"), llm.ErrNonRetryable)
	a = &Operations{Proposer: &proposeRunner{err: refused}}
	_, err = a.ProposeProfile(context.Background(), ProposeProfileInput{Name: "Clinic"})
	if !errors.As(err, &appErr) {
		t.Fatalf("want PermanentError for refusal, got %T: %v", err, err)
	}
}

func TestProposeProfileHappyPath(t *testing.T) {
	runner := &proposeRunner{result: llm.ProposeProfileRunResult{RawJSON: validProposalJSON(t), Model: "gpt-x"}}
	a := &Operations{Proposer: runner}
	out, err := a.ProposeProfile(context.Background(), ProposeProfileInput{Name: "Novena Ortho Clinic"})
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
