package workflows

import (
	"context"
	"log/slog"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/log"
)

// actLogger returns the Temporal activity logger when ctx is a real activity
// context and the process logger otherwise. Activity bodies are also called
// directly from unit tests with a plain context, where activity.GetLogger
// panics; *slog.Logger satisfies Temporal's log.Logger, so callers see one API.
func actLogger(ctx context.Context) log.Logger {
	if activity.IsActivity(ctx) {
		return activity.GetLogger(ctx)
	}
	return slog.Default()
}
