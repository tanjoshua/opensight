package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
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

func TestRunTenantCreateDispatches(t *testing.T) {
	var out bytes.Buffer
	var got tenantCreateOptions
	called := 0

	deps := commandDeps{
		stdout: &out,
		createTenant: func(_ context.Context, _ config.Config, opts tenantCreateOptions, w io.Writer) error {
			called++
			got = opts
			_, _ = fmt.Fprintln(w, "created")
			return nil
		},
	}

	if err := runWithDeps(context.Background(), []string{"tenant", "create", "--name", "  Acme Clinic  "}, deps); err != nil {
		t.Fatalf("tenant create returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("createTenant called %d times, want 1", called)
	}
	if got.Name != "Acme Clinic" {
		t.Fatalf("tenant name = %q, want %q", got.Name, "Acme Clinic")
	}
	if out.String() != "created\n" {
		t.Fatalf("stdout = %q, want created line", out.String())
	}
}

func TestRunSeedDevDispatches(t *testing.T) {
	var out bytes.Buffer
	called := 0
	deps := commandDeps{
		stdout: &out,
		seedDev: func(_ context.Context, _ config.Config, w io.Writer) error {
			called++
			_, _ = fmt.Fprintln(w, "seeded")
			return nil
		},
	}

	if err := runWithDeps(context.Background(), []string{"seed", "dev"}, deps); err != nil {
		t.Fatalf("seed dev returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("seedDev called %d times, want 1", called)
	}
	if out.String() != "seeded\n" {
		t.Fatalf("stdout = %q, want seeded line", out.String())
	}
}

func TestRunSeedRejectsUnknownSubcommand(t *testing.T) {
	deps := commandDeps{
		seedDev: func(context.Context, config.Config, io.Writer) error {
			t.Fatal("seedDev should not be called")
			return nil
		},
	}

	if err := runWithDeps(context.Background(), []string{"seed", "prod"}, deps); err == nil {
		t.Fatal("expected error for unknown seed subcommand")
	}
}

func TestRunTenantCreateRequiresName(t *testing.T) {
	deps := commandDeps{
		createTenant: func(context.Context, config.Config, tenantCreateOptions, io.Writer) error {
			t.Fatal("createTenant should not be called")
			return nil
		},
	}

	err := runWithDeps(context.Background(), []string{"tenant", "create"}, deps)
	if err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("tenant create error = %v, want missing-name error", err)
	}
}

func TestRunUserCreateDispatchesWithStdinPassword(t *testing.T) {
	var got userCreateOptions
	called := 0
	generateCalled := false

	deps := commandDeps{
		stdin: strings.NewReader("set-password\n"),
		createUser: func(_ context.Context, _ config.Config, opts userCreateOptions, _ io.Writer) error {
			called++
			got = opts
			return nil
		},
		generatePassword: func() (string, error) {
			generateCalled = true
			return "generated-password", nil
		},
	}

	err := runWithDeps(context.Background(), []string{
		"user", "create",
		"--tenant", tenantIDForTest,
		"--email", "  Owner@Example.com  ",
		"--password-stdin",
	}, deps)
	if err != nil {
		t.Fatalf("user create returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("createUser called %d times, want 1", called)
	}
	if generateCalled {
		t.Fatal("generatePassword was called despite --password")
	}
	if got.TenantID.String() != tenantIDForTest {
		t.Fatalf("tenant id = %s, want %s", got.TenantID, tenantIDForTest)
	}
	if got.Email != "Owner@Example.com" {
		t.Fatalf("email = %q, want trimmed email preserving case", got.Email)
	}
	if got.Password != "set-password" {
		t.Fatalf("password = %q, want stdin password", got.Password)
	}
	if got.GeneratedPassword {
		t.Fatal("GeneratedPassword = true for stdin password")
	}
}

func TestRunUserCreateGeneratesPasswordByDefault(t *testing.T) {
	var got userCreateOptions
	deps := commandDeps{
		createUser: func(_ context.Context, _ config.Config, opts userCreateOptions, _ io.Writer) error {
			got = opts
			return nil
		},
		generatePassword: func() (string, error) {
			return "generated-password", nil
		},
	}

	err := runWithDeps(context.Background(), []string{
		"user", "create",
		"--tenant", tenantIDForTest,
		"--email", "owner@example.com",
	}, deps)
	if err != nil {
		t.Fatalf("user create returned error: %v", err)
	}
	if got.Password != "generated-password" {
		t.Fatalf("password = %q, want generated password", got.Password)
	}
	if !got.GeneratedPassword {
		t.Fatal("GeneratedPassword = false, want true")
	}
}

func TestRunUserCreateRejectsEmptyStdinPassword(t *testing.T) {
	deps := commandDeps{
		stdin: strings.NewReader("\n"),
		createUser: func(context.Context, config.Config, userCreateOptions, io.Writer) error {
			t.Fatal("createUser should not be called")
			return nil
		},
	}

	err := runWithDeps(context.Background(), []string{
		"user", "create",
		"--tenant", tenantIDForTest,
		"--email", "owner@example.com",
		"--password-stdin",
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "password from stdin is required") {
		t.Fatalf("user create error = %v, want empty-stdin error", err)
	}
}

func TestRunUserCreateRequiresTenantUUIDAndEmail(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing tenant",
			args: []string{"user", "create", "--email", "owner@example.com"},
			want: "--tenant is required",
		},
		{
			name: "bad tenant",
			args: []string{"user", "create", "--tenant", "not-a-uuid", "--email", "owner@example.com"},
			want: "--tenant must be a UUID",
		},
		{
			name: "missing email",
			args: []string{"user", "create", "--tenant", tenantIDForTest},
			want: "--email is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runWithDeps(context.Background(), tc.args, commandDeps{
				createUser: func(context.Context, config.Config, userCreateOptions, io.Writer) error {
					t.Fatal("createUser should not be called")
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestGeneratePasswordShape(t *testing.T) {
	first, err := generatePassword()
	if err != nil {
		t.Fatalf("generatePassword: %v", err)
	}
	second, err := generatePassword()
	if err != nil {
		t.Fatalf("second generatePassword: %v", err)
	}
	if len(first) != 24 {
		t.Fatalf("password length = %d, want 24", len(first))
	}
	if first == second {
		t.Fatal("two generated passwords are identical")
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

const tenantIDForTest = "01950000-0000-7000-8000-0000000000b2"
