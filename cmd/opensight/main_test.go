package main

import (
	"bytes"
	"context"
	"encoding/json"
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
		{"missing account subcommand", []string{"account"}, "account subcommand required"},
		{"unknown account subcommand", []string{"account", "bogus"}, "unknown account subcommand"},
		{"missing account member subcommand", []string{"account", "member"}, "account member subcommand"},
		{"unknown account member subcommand", []string{"account", "member", "bogus"}, "account member subcommand"},
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

func TestValidateServeRuntimeConfig(t *testing.T) {
	complete := config.Config{
		StripeSecretKey:             "sk_test_123",
		StripeWebhookSecret:         "whsec_123",
		StripePortalConfigurationID: "bpc_123",
		StripePriceIDs:              map[string]string{"starter": "price_123"},
		AppBaseURL:                  "https://app.example.com",
		GoogleClientID:              "client-id",
		GoogleClientSecret:          "client-secret",
	}
	if err := validateServeRuntimeConfig(complete); err != nil {
		t.Fatalf("complete config: %v", err)
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
		{"google client id", func(c *config.Config) { c.GoogleClientID = "" }, "GOOGLE_CLIENT_ID"},
		{"google client secret", func(c *config.Config) { c.GoogleClientSecret = "" }, "GOOGLE_CLIENT_SECRET"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := complete
			tc.edit(&cfg)
			if err := validateServeRuntimeConfig(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseAccountCreateArgs(t *testing.T) {
	opts, err := parseAccountCreateArgs([]string{"--name", "  Acme Clinic  "})
	if err != nil {
		t.Fatalf("parseAccountCreateArgs: %v", err)
	}
	if opts.Name != "Acme Clinic" {
		t.Fatalf("account name = %q, want %q", opts.Name, "Acme Clinic")
	}

	if _, err := parseAccountCreateArgs(nil); err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("error = %v, want missing-name error", err)
	}
}

func TestParseAccountMemberAddArgs(t *testing.T) {
	opts, err := parseAccountMemberAddArgs([]string{
		"--account", accountIDForTest,
		"--email", "  Owner@Example.com  ",
		"--role", "admin",
	})
	if err != nil {
		t.Fatalf("parseAccountMemberAddArgs: %v", err)
	}
	if opts.AccountID.String() != accountIDForTest {
		t.Fatalf("account id = %s, want %s", opts.AccountID, accountIDForTest)
	}
	if opts.Email != "Owner@Example.com" {
		t.Fatalf("email = %q, want trimmed email preserving case", opts.Email)
	}
	if opts.Role != "admin" {
		t.Fatalf("role = %q, want admin", opts.Role)
	}
}

func TestParseAccountMemberAddArgsRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing account", []string{"--email", "owner@example.com", "--role", "member"}, "--account is required"},
		{"bad account", []string{"--account", "not-a-uuid", "--email", "owner@example.com", "--role", "member"}, "--account must be a UUID"},
		{"missing email", []string{"--account", accountIDForTest, "--role", "member"}, "--email is required"},
		{"missing role", []string{"--account", accountIDForTest, "--email", "owner@example.com"}, "--role must be"},
		{"bad role", []string{"--account", accountIDForTest, "--email", "owner@example.com", "--role", "superuser"}, "--role must be"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseAccountMemberAddArgs(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseSeedDevArgs(t *testing.T) {
	noEnv := func(string) string { return "" }

	got, err := parseSeedDevArgs([]string{"--email", "  Owner@Example.com  "}, noEnv)
	if err != nil {
		t.Fatalf("parseSeedDevArgs: %v", err)
	}
	if got != "Owner@Example.com" {
		t.Fatalf("email = %q, want trimmed email preserving case", got)
	}

	envFallback := func(key string) string {
		if key == "OPENSIGHT_DEV_EMAIL" {
			return "dev@example.com"
		}
		return ""
	}
	if got, err := parseSeedDevArgs(nil, envFallback); err != nil || got != "dev@example.com" {
		t.Fatalf("parseSeedDevArgs(nil) = (%q, %v), want (\"dev@example.com\", nil)", got, err)
	}

	if _, err := parseSeedDevArgs(nil, noEnv); err == nil || !strings.Contains(err.Error(), "--email or OPENSIGHT_DEV_EMAIL is required") {
		t.Fatalf("error = %v, want missing-email error", err)
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

const accountIDForTest = "01950000-0000-7000-8000-0000000000b2"
