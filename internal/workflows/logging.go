package workflows

import (
	"context"
	"log/slog"
)

// actLogger returns the process logger used by operations and focused tests.
func actLogger(context.Context) *slog.Logger { return slog.Default() }
