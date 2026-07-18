package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"opensight/internal/config"
)

func TestRunKnownSubcommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, cmd := range []string{"serve", "work", "migrate"} {
		if err := run(ctx, []string{cmd}); err != nil {
			t.Errorf("run(%q) returned error: %v", cmd, err)
		}
	}
}

func TestRunNoSubcommand(t *testing.T) {
	if err := run(context.Background(), nil); err == nil {
		t.Fatal("expected error when no subcommand is given")
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	if err := run(context.Background(), []string{"bogus"}); err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

func TestRunDoesNotMigrateOnServeOrWorkStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	deps := commandDeps{
		migrate: func(context.Context, config.Config) error {
			t.Fatal("migrate should not be called by serve or work")
			return nil
		},
	}

	for _, cmd := range []string{"serve", "work"} {
		if err := runWithDeps(ctx, []string{cmd}, deps); err != nil {
			t.Errorf("runWithDeps(%q) returned error: %v", cmd, err)
		}
	}
}

func TestRunMigrateUsesExplicitMigrateSubcommand(t *testing.T) {
	wantErr := errors.New("sentinel")
	called := 0

	deps := commandDeps{
		migrate: func(context.Context, config.Config) error {
			called++
			return wantErr
		},
	}

	err := runWithDeps(context.Background(), []string{"migrate"}, deps)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runWithDeps(migrate) error = %v, want %v", err, wantErr)
	}
	if called != 1 {
		t.Fatalf("migrate called %d times, want 1", called)
	}
}

func TestNewLoggerEmitsJSON(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf).Info("hello", "key", "value")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not valid JSON: %v (output: %q)", err, buf.String())
	}
	if line["msg"] != "hello" {
		t.Errorf("msg = %v, want %q", line["msg"], "hello")
	}
	if line["key"] != "value" {
		t.Errorf("key = %v, want %q", line["key"], "value")
	}
}
