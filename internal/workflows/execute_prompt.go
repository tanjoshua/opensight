package workflows

import (
	"context"
	"errors"
	"time"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"
)

// MaxExecutePromptAttempts is the retry budget for a single prompt execution
// (design 04: 4 attempts). It is also the terminal-failure boundary the
// operation enforces itself before the parent River job continues.
const MaxExecutePromptAttempts = 4

// ExecutePromptInput is one prompt execution for a run.
type ExecutePromptInput struct {
	AccountID domain.ID `json:"TenantID"`
	RunID     domain.ID
	Prompt    PromptSnapshot
	Location  llm.Location
}

// ExecutePromptOutput is the recorded result of a prompt execution.
type ExecutePromptOutput struct {
	ResultID domain.ID
	Status   store.ResultStatus
}

// isTerminalFailure reports whether a RunPrompt error should stop retrying: a
// non-retryable runner error (400-class request errors, content-policy
// refusals) or an exhausted retry budget. Kept pure for unit testing.
func isTerminalFailure(err error, attempt, maxAttempts int32) bool {
	return errors.Is(err, llm.ErrNonRetryable) || attempt >= maxAttempts
}

// ExecutePrompt runs one prompt and records exactly one prompt_results row
// (design 04). It is idempotent: an existing row for (run_id, prompt_id) is
// returned without a second LLM call, covering retries after a success whose
// ack was lost.
//
// Retry semantics: a transient failure with attempts left persists nothing and
// retries locally. A terminal failure (non-retryable,
// or the final attempt of a transient one) durably records a failed result and
// returns nil — a refusal is a recorded finding, not an outage to keep retrying.
func (a *Operations) ExecutePrompt(ctx context.Context, in ExecutePromptInput) (ExecutePromptOutput, error) {
	if existing, err := a.Store.GetResultByRunAndPrompt(ctx, in.AccountID, in.RunID, in.Prompt.ID); err == nil {
		return ExecutePromptOutput{ResultID: existing.ID, Status: existing.Status}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return ExecutePromptOutput{}, err
	}

	var result llm.PromptRunResult
	var runErr error
	for attempt := 1; attempt <= MaxExecutePromptAttempts; attempt++ {
		if err := a.Limiter.Acquire(ctx); err != nil {
			return ExecutePromptOutput{}, err
		}
		result, runErr = a.Runner.RunPrompt(ctx, llm.PromptRequest{Prompt: in.Prompt.Text, Location: in.Location})
		a.Limiter.Release()
		if runErr == nil || errors.Is(runErr, llm.ErrNonRetryable) || attempt == MaxExecutePromptAttempts {
			break
		}
		timer := time.NewTimer(time.Duration(1<<(attempt-1)) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ExecutePromptOutput{}, ctx.Err()
		case <-timer.C:
		}
	}
	if runErr == nil {
		created, err := a.createResult(ctx, in, store.CreateResultParams{
			RunID:        in.RunID,
			PromptID:     in.Prompt.ID,
			Status:       store.ResultStatusSucceeded,
			Model:        &result.Model,
			Request:      result.RequestJSON,
			RawResponse:  result.RawResponse,
			ResponseText: &result.ResponseText,
		})
		if err != nil {
			return ExecutePromptOutput{}, err
		}
		return ExecutePromptOutput{ResultID: created.ID, Status: created.Status}, nil
	}

	// Terminal failure: durably record a failed result. Return nil so River
	// can continue — the failure is recorded, and retrying
	// would violate "a refusal is a recorded failed result, not a retry."
	errMsg := runErr.Error()
	created, err := a.createResult(ctx, in, store.CreateResultParams{
		RunID:    in.RunID,
		PromptID: in.Prompt.ID,
		Status:   store.ResultStatusFailed,
		Request:  result.RequestJSON,
		Error:    &errMsg,
	})
	if err != nil {
		return ExecutePromptOutput{}, err
	}
	return ExecutePromptOutput{ResultID: created.ID, Status: created.Status}, nil
}

// createResult writes the result, treating an ErrDuplicateResult race (a
// concurrent attempt won) as success by re-fetching the winning row.
func (a *Operations) createResult(ctx context.Context, in ExecutePromptInput, params store.CreateResultParams) (store.PromptResult, error) {
	created, err := a.Store.CreateResult(ctx, in.AccountID, params)
	if errors.Is(err, store.ErrDuplicateResult) {
		return a.Store.GetResultByRunAndPrompt(ctx, in.AccountID, in.RunID, in.Prompt.ID)
	}
	return created, err
}
