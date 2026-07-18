// Command opensight is the single OpenSight binary. It runs in one of three
// modes selected by subcommand:
//
//	opensight serve     # HTTP API server (01-D2)
//	opensight work      # Temporal worker (01-D2)
//	opensight migrate   # apply database migrations, then exit (07)
//
// The same image runs serve and work via a command override in deployment
// (07 "Deployment"). For FND-1 the subcommands are no-op stubs; later stories
// wire in the real server, worker, and migrations.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

const usage = "usage: opensight <serve|work|migrate>"

func main() {
	slog.SetDefault(newLogger(os.Stdout))

	if err := run(os.Args[1:]); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

// newLogger builds the default structured JSON logger writing to w. Structured
// slog JSON to stdout is the whole logging story for MVP (07 "Observability").
func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// run dispatches the chosen subcommand. It is separated from main so it can be
// tested without spawning the process.
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no subcommand given; %s", usage)
	}

	switch cmd := args[0]; cmd {
	case "serve":
		return serve()
	case "work":
		return work()
	case "migrate":
		return migrate()
	default:
		return fmt.Errorf("unknown subcommand %q; %s", cmd, usage)
	}
}

// serve will run the HTTP API server. Stub for FND-1.
func serve() error {
	slog.Info("serve: not yet implemented", "mode", "serve")
	return nil
}

// work will run the Temporal worker. Stub for FND-1.
func work() error {
	slog.Info("work: not yet implemented", "mode", "work")
	return nil
}

// migrate will apply database migrations and exit. Stub for FND-1.
func migrate() error {
	slog.Info("migrate: not yet implemented", "mode", "migrate")
	return nil
}
