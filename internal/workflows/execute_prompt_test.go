package workflows

import (
	"errors"
	"fmt"
	"testing"

	"opensight/internal/llm"
)

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
