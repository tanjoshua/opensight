// Command opensight is the single OpenSight binary. It runs in one of three
// modes selected by subcommand:
//
//	opensight serve     # HTTP API server (01-D2)
//	opensight work      # Temporal worker (01-D2)
//	opensight migrate   # apply database migrations, then exit (07)
//
// The same image runs serve and work via a command override in deployment
// (07 "Deployment"). Foundation stories wire the first health server, Temporal
// connection, and migration stub; later stories add real routes, workflows, and
// migrations.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"opensight/internal/config"

	"go.temporal.io/sdk/client"
)

const usage = "usage: opensight <serve|work|migrate>"

func main() {
	slog.SetDefault(newLogger(os.Stdout))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
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
func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no subcommand given; %s", usage)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch cmd := args[0]; cmd {
	case "serve":
		return serve(ctx, cfg)
	case "work":
		return work(ctx, cfg)
	case "migrate":
		return migrate(cfg)
	default:
		return fmt.Errorf("unknown subcommand %q; %s", cmd, usage)
	}
}

// serve runs the HTTP API server.
func serve(ctx context.Context, cfg config.Config) error {
	if ctx.Err() != nil {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info(
			"serve: starting",
			"mode", "serve",
			"http_addr", cfg.HTTPAddr,
			"env", cfg.Env,
			"prompt_runner_mode", cfg.PromptRunnerMode,
		)

		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}

		select {
		case err := <-errCh:
			return err
		case <-shutdownCtx.Done():
			return shutdownCtx.Err()
		}
	case err := <-errCh:
		return err
	}
}

// work connects to Temporal and blocks until shutdown. Workflows are registered
// by later stories.
func work(ctx context.Context, cfg config.Config) error {
	if ctx.Err() != nil {
		return nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	temporalClient, err := client.DialContext(dialCtx, client.Options{
		HostPort:  cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		return fmt.Errorf("connect temporal: %w", err)
	}
	defer temporalClient.Close()

	slog.Info(
		"work: connected to temporal",
		"mode", "work",
		"temporal_address", cfg.TemporalAddress,
		"temporal_namespace", cfg.TemporalNamespace,
		"temporal_task_queue", cfg.TemporalTaskQueue,
	)

	<-ctx.Done()
	return nil
}

// migrate will apply database migrations and exit. Stub for FND-1.
func migrate(cfg config.Config) error {
	slog.Info(
		"migrate: not yet implemented",
		"mode", "migrate",
		"db_max_open_conns", cfg.DBMaxOpenConns,
		"db_max_idle_conns", cfg.DBMaxIdleConns,
	)
	return nil
}
