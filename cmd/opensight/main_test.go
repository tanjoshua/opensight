package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
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
