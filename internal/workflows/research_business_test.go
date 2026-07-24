package workflows

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opensight/internal/llm"

	"go.temporal.io/sdk/temporal"
)

// researchRunner is a PromptRunner test double: it captures the request it
// received and returns a canned result or error.
type researchRunner struct {
	req    llm.PromptRequest
	result llm.PromptRunResult
	err    error
}

func (r *researchRunner) RunPrompt(_ context.Context, req llm.PromptRequest) (llm.PromptRunResult, error) {
	r.req = req
	return r.result, r.err
}

func TestResearchBusinessPromptCarriesNameAndLocation(t *testing.T) {
	runner := &researchRunner{result: llm.PromptRunResult{ResponseText: "  findings  ", Model: "gpt-x"}}
	a := &Activities{Runner: runner}
	loc := llm.Location{Country: "SG", City: "Singapore", Region: "Novena"}

	out, err := a.ResearchBusiness(context.Background(), ResearchBusinessInput{
		Name:     "Novena Ortho Clinic",
		Location: loc,
	})
	if err != nil {
		t.Fatalf("ResearchBusiness: %v", err)
	}
	if out.Summary != "findings" {
		t.Fatalf("Summary = %q, want trimmed %q", out.Summary, "findings")
	}
	if out.Model != "gpt-x" {
		t.Fatalf("Model = %q, want %q", out.Model, "gpt-x")
	}

	// The web_search location is passed through for search grounding.
	if runner.req.Location != loc {
		t.Fatalf("Location = %+v, want %+v", runner.req.Location, loc)
	}
	// The prompt text carries the name and the location hints.
	for _, want := range []string{"Novena Ortho Clinic", "Singapore", "Novena", "SG"} {
		if !strings.Contains(runner.req.Prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, runner.req.Prompt)
		}
	}
	// The prompt asks for the business's specialty/category, not just aliases
	// and directory listings — this is the only category evidence
	// ProposeProfile gets when FetchSite fails, and its absence previously let
	// the model default to an unrelated example category.
	if !strings.Contains(runner.req.Prompt, "specialty") {
		t.Fatalf("prompt missing specialty ask:\n%s", runner.req.Prompt)
	}
}

func TestResearchBusinessEmptyNameNonRetryable(t *testing.T) {
	runner := &researchRunner{}
	a := &Activities{Runner: runner}

	_, err := a.ResearchBusiness(context.Background(), ResearchBusinessInput{Name: "  "})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("want non-retryable ApplicationError, got %T: %v", err, err)
	}
	if runner.req.Prompt != "" {
		t.Fatal("runner should not be called for bad input")
	}
}

func TestResearchBusinessRunnerErrorPropagates(t *testing.T) {
	// A transient runner error propagates unchanged for Temporal's default retry.
	transient := errors.New("openai: status 503")
	a := &Activities{Runner: &researchRunner{err: transient}}
	_, err := a.ResearchBusiness(context.Background(), ResearchBusinessInput{
		Name:     "Clinic",
		Location: llm.Location{Country: "SG"},
	})
	if !errors.Is(err, transient) {
		t.Fatalf("want transient error propagated, got %v", err)
	}
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.NonRetryable() {
		t.Fatal("transient error must not be wrapped non-retryable")
	}

	// A runner error marked non-retryable becomes a non-retryable activity error.
	refused := errors.New("refused: " + llm.ErrNonRetryable.Error())
	a = &Activities{Runner: &researchRunner{err: errors.Join(refused, llm.ErrNonRetryable)}}
	_, err = a.ResearchBusiness(context.Background(), ResearchBusinessInput{
		Name:     "Clinic",
		Location: llm.Location{Country: "SG"},
	})
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("want non-retryable ApplicationError for refusal, got %T: %v", err, err)
	}
}

func TestResearchBusinessEmptyResponse(t *testing.T) {
	// A successful call with no text yields an empty summary and no error; the
	// workflow's failure posture (ONB-4), not this activity, decides if that is
	// enough.
	a := &Activities{Runner: &researchRunner{result: llm.PromptRunResult{ResponseText: "   ", Model: "gpt-x"}}}
	out, err := a.ResearchBusiness(context.Background(), ResearchBusinessInput{
		Name:     "Clinic",
		Location: llm.Location{Country: "SG"},
	})
	if err != nil {
		t.Fatalf("ResearchBusiness: %v", err)
	}
	if out.Summary != "" {
		t.Fatalf("Summary = %q, want empty", out.Summary)
	}
}
