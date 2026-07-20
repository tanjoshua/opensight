package workflows

import (
	"context"
	"errors"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"

	"go.temporal.io/sdk/activity"
)

// MaxExecutePromptAttempts is the retry budget for a single prompt execution
// (design 04: 4 attempts). It is also the terminal-failure boundary the
// activity enforces itself, so the RetryPolicy and this constant must agree.
const MaxExecutePromptAttempts = 4

// ExecutePromptInput is one prompt execution for a run.
type ExecutePromptInput struct {
	TenantID domain.ID
	RunID    domain.ID
	Prompt   PromptSnapshot
	Location llm.Location
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
// returns the error so Temporal reschedules. A terminal failure (non-retryable,
// or the final attempt of a transient one) durably records a failed result and
// returns nil — a refusal is a recorded finding, not an outage to keep retrying.
func (a *Activities) ExecutePrompt(ctx context.Context, in ExecutePromptInput) (ExecutePromptOutput, error) {
	if existing, err := a.Results.GetResultByRunAndPrompt(ctx, in.TenantID, in.RunID, in.Prompt.ID); err == nil {
		return ExecutePromptOutput{ResultID: existing.ID, Status: existing.Status}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return ExecutePromptOutput{}, err
	}

	result, runErr := a.Runner.RunPrompt(ctx, llm.PromptRequest{
		Prompt:   in.Prompt.Text,
		Location: in.Location,
	})
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

	attempt := activity.GetInfo(ctx).Attempt
	if !isTerminalFailure(runErr, attempt, MaxExecutePromptAttempts) {
		// Transient, attempts remain: persist nothing and let Temporal retry.
		return ExecutePromptOutput{}, runErr
	}

	// Terminal failure: durably record a failed result. Return nil so Temporal
	// sees the activity as complete — the failure is recorded, and retrying
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
// concurrent activity attempt won) as success by re-fetching the winning row.
func (a *Activities) createResult(ctx context.Context, in ExecutePromptInput, params store.CreateResultParams) (store.PromptResult, error) {
	created, err := a.Results.CreateResult(ctx, in.TenantID, params)
	if errors.Is(err, store.ErrDuplicateResult) {
		return a.Results.GetResultByRunAndPrompt(ctx, in.TenantID, in.RunID, in.Prompt.ID)
	}
	return created, err
}
