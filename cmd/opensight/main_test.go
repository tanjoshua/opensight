package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"opensight/internal/config"
)

func TestRunKnownSubcommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// serve, work, and migrate all short-circuit on a cancelled context before
	// touching the database or Temporal, so this exercises dispatch only.
	for _, cmd := range []string{"serve", "work", "migrate"} {
		if err := run(ctx, []string{cmd}); err != nil {
			t.Errorf("run(%q) returned error: %v", cmd, err)
		}
	}
}

func TestRunRejectsUnknownAndMissingSubcommands(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no subcommand", nil, "no subcommand given"},
		{"unknown subcommand", []string{"bogus"}, "unknown subcommand"},
		{"missing tenant subcommand", []string{"tenant"}, "tenant subcommand required"},
		{"unknown tenant subcommand", []string{"tenant", "bogus"}, "unknown tenant subcommand"},
		{"missing user subcommand", []string{"user"}, "user subcommand required"},
		{"unknown user subcommand", []string{"user", "bogus"}, "unknown user subcommand"},
		{"missing business subcommand", []string{"business"}, "business subcommand required"},
		{"unknown business subcommand", []string{"business", "bogus"}, "unknown business subcommand"},
		{"missing seed subcommand", []string{"seed"}, "seed subcommand required"},
		{"unknown seed subcommand", []string{"seed", "prod"}, "unknown seed subcommand"},
		{"seed dev extra argument", []string{"seed", "dev", "extra"}, "unexpected argument"},
		{"missing stripe subcommand", []string{"stripe"}, "stripe subcommand required"},
		{"unknown stripe subcommand", []string{"stripe", "bogus"}, "unknown stripe subcommand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(context.Background(), tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateStripeRuntimeConfig(t *testing.T) {
	complete := config.Config{
		StripeSecretKey:             "sk_test_123",
		StripeWebhookSecret:         "whsec_123",
		StripePortalConfigurationID: "bpc_123",
		StripePriceIDs:              map[string]string{"starter": "price_123"},
		AppBaseURL:                  "https://app.example.com",
	}
	if err := validateStripeRuntimeConfig(complete); err != nil {
		t.Fatalf("complete Stripe config: %v", err)
	}

	cases := []struct {
		name string
		edit func(*config.Config)
		want string
	}{
		{"secret key", func(c *config.Config) { c.StripeSecretKey = "" }, "STRIPE_SECRET_KEY"},
		{"webhook secret", func(c *config.Config) { c.StripeWebhookSecret = "" }, "STRIPE_WEBHOOK_SECRET"},
		{"portal configuration", func(c *config.Config) { c.StripePortalConfigurationID = "" }, "STRIPE_PORTAL_CONFIGURATION_ID"},
		{"app base URL", func(c *config.Config) { c.AppBaseURL = "" }, "APP_BASE_URL"},
		{"price", func(c *config.Config) { c.StripePriceIDs = nil }, "STRIPE_PRICE_STARTER_MONTHLY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := complete
			tc.edit(&cfg)
			if err := validateStripeRuntimeConfig(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseTenantCreateArgs(t *testing.T) {
	opts, err := parseTenantCreateArgs([]string{"--name", "  Acme Clinic  "})
	if err != nil {
		t.Fatalf("parseTenantCreateArgs: %v", err)
	}
	if opts.Name != "Acme Clinic" {
		t.Fatalf("tenant name = %q, want %q", opts.Name, "Acme Clinic")
	}

	if _, err := parseTenantCreateArgs(nil); err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("error = %v, want missing-name error", err)
	}
}

func TestParseUserCreateArgsStdinPassword(t *testing.T) {
	generateCalled := false
	generate := func() (string, error) {
		generateCalled = true
		return "generated-password", nil
	}

	opts, err := parseUserCreateArgs([]string{
		"--tenant", tenantIDForTest,
		"--email", "  Owner@Example.com  ",
		"--password-stdin",
	}, generate, strings.NewReader("set-password\n"))
	if err != nil {
		t.Fatalf("parseUserCreateArgs: %v", err)
	}
	if generateCalled {
		t.Fatal("generatePassword was called despite --password-stdin")
	}
	if opts.TenantID.String() != tenantIDForTest {
		t.Fatalf("tenant id = %s, want %s", opts.TenantID, tenantIDForTest)
	}
	if opts.Email != "Owner@Example.com" {
		t.Fatalf("email = %q, want trimmed email preserving case", opts.Email)
	}
	if opts.Password != "set-password" {
		t.Fatalf("password = %q, want stdin password", opts.Password)
	}
	if opts.GeneratedPassword {
		t.Fatal("GeneratedPassword = true for stdin password")
	}
}

func TestParseUserCreateArgsGeneratesPasswordByDefault(t *testing.T) {
	generate := func() (string, error) { return "generated-password", nil }

	opts, err := parseUserCreateArgs([]string{
		"--tenant", tenantIDForTest,
		"--email", "owner@example.com",
	}, generate, nil)
	if err != nil {
		t.Fatalf("parseUserCreateArgs: %v", err)
	}
	if opts.Password != "generated-password" {
		t.Fatalf("password = %q, want generated password", opts.Password)
	}
	if !opts.GeneratedPassword {
		t.Fatal("GeneratedPassword = false, want true")
	}
}

func TestParseUserCreateArgsRejectsBadInput(t *testing.T) {
	generate := func() (string, error) { return "generated-password", nil }

	for _, tc := range []struct {
		name  string
		args  []string
		stdin io.Reader
		want  string
	}{
		{"missing tenant", []string{"--email", "owner@example.com"}, nil, "--tenant is required"},
		{"bad tenant", []string{"--tenant", "not-a-uuid", "--email", "owner@example.com"}, nil, "--tenant must be a UUID"},
		{"missing email", []string{"--tenant", tenantIDForTest}, nil, "--email is required"},
		{
			name:  "empty stdin password",
			args:  []string{"--tenant", tenantIDForTest, "--email", "owner@example.com", "--password-stdin"},
			stdin: strings.NewReader("\n"),
			want:  "password from stdin is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseUserCreateArgs(tc.args, generate, tc.stdin); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestReadPasswordFromStdinRejectsOverlongInput(t *testing.T) {
	if _, err := readPasswordFromStdin(strings.NewReader(strings.Repeat("a", 2049))); err == nil {
		t.Fatal("expected error for an over-long stdin password")
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
