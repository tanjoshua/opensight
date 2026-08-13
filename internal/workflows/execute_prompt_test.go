package workflows

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opensight/internal/llm"
)

type promptRunnerFunc func(context.Context, llm.PromptRequest) (llm.PromptRunResult, error)

func (f promptRunnerFunc) RunPrompt(ctx context.Context, req llm.PromptRequest) (llm.PromptRunResult, error) {
	return f(ctx, req)
}

func TestIsTerminalFailure(t *testing.T) {
	transient := errors.New("openai: status 503")
	nonRetryable := fmt.Errorf("refused: %w", llm.ErrNonRetryable)

	cases := []struct {
		name        string
		err         error
		attempt     int32
		maxAttempts int32
		want        bool
	}{
		{"transient with attempts left", transient, 1, MaxExecutePromptAttempts, false},
		{"transient on last attempt", transient, MaxExecutePromptAttempts, MaxExecutePromptAttempts, true},
		{"transient past budget", transient, MaxExecutePromptAttempts + 1, MaxExecutePromptAttempts, true},
		{"non-retryable on first attempt", nonRetryable, 1, MaxExecutePromptAttempts, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTerminalFailure(tc.err, tc.attempt, tc.maxAttempts); got != tc.want {
				t.Fatalf("isTerminalFailure(%v, %d, %d) = %v, want %v",
					tc.err, tc.attempt, tc.maxAttempts, got, tc.want)
			}
		})
	}
}

func TestRunPromptAttemptsRetriesAfterAttemptTimeout(t *testing.T) {
	if executePromptAttemptTimeout != 120*time.Second {
		t.Fatalf("executePromptAttemptTimeout = %s, want 120s", executePromptAttemptTimeout)
	}

	calls := 0
	runner := promptRunnerFunc(func(ctx context.Context, _ llm.PromptRequest) (llm.PromptRunResult, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("RunPrompt context has no attempt deadline")
		}
		if calls == 1 {
			<-ctx.Done()
			return llm.PromptRunResult{}, ctx.Err()
		}
		return llm.PromptRunResult{ResponseText: "ok"}, nil
	})
	ops := &Operations{Runner: runner, Limiter: llm.NewLimiter(1)}

	result, err := ops.runPromptAttempts(context.Background(), llm.PromptRequest{}, 10*time.Millisecond, func(int) time.Duration { return 0 })
	if err != nil {
		t.Fatalf("runPromptAttempts: %v", err)
	}
	if calls != 2 {
		t.Fatalf("RunPrompt calls = %d, want 2", calls)
	}
	if result.ResponseText != "ok" {
		t.Fatalf("response text = %q, want ok", result.ResponseText)
	}
}

func TestRunPromptAttemptsDoesNotRetryParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	runner := promptRunnerFunc(func(callCtx context.Context, _ llm.PromptRequest) (llm.PromptRunResult, error) {
		calls++
		cancel()
		<-callCtx.Done()
		return llm.PromptRunResult{}, callCtx.Err()
	})
	ops := &Operations{Runner: runner, Limiter: llm.NewLimiter(1)}

	_, err := ops.runPromptAttempts(ctx, llm.PromptRequest{}, time.Second, func(int) time.Duration { return 0 })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runPromptAttempts error = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("RunPrompt calls = %d, want 1", calls)
	}
}
