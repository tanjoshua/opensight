package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	billingstripe "opensight/internal/billing/stripe"
	"opensight/internal/config"
)

func TestWritePortalConfigResult(t *testing.T) {
	var out bytes.Buffer
	if err := writePortalConfigResult(&out, "bpc_123"); err != nil {
		t.Fatalf("writePortalConfigResult: %v", err)
	}
	if got, want := out.String(), "STRIPE_PORTAL_CONFIGURATION_ID=bpc_123\naction=updated\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestApplyPortalConfigCLIRequiresConfigurationID(t *testing.T) {
	err := applyPortalConfigCLI(context.Background(), config.Config{
		StripeSecretKey: "sk_test_123",
		AppBaseURL:      "https://app.example.com",
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "STRIPE_PORTAL_CONFIGURATION_ID") {
		t.Fatalf("error = %v, want missing configuration id", err)
	}
}

func TestWriteWebhookConfigResultCreated(t *testing.T) {
	var out bytes.Buffer
	err := writeWebhookConfigResult(&out, billingstripe.WebhookEndpointResult{
		ID: "we_123", Secret: "whsec_abc", Created: true,
	})
	if err != nil {
		t.Fatalf("writeWebhookConfigResult: %v", err)
	}
	if got, want := out.String(), "id=we_123\naction=created\nSTRIPE_WEBHOOK_SECRET=whsec_abc\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestWriteWebhookConfigResultUpdated(t *testing.T) {
	var out bytes.Buffer
	err := writeWebhookConfigResult(&out, billingstripe.WebhookEndpointResult{ID: "we_123"})
	if err != nil {
		t.Fatalf("writeWebhookConfigResult: %v", err)
	}
	if got, want := out.String(), "id=we_123\naction=updated\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestApplyWebhookConfigCLIRequiresAppBaseURL(t *testing.T) {
	err := applyWebhookConfigCLI(context.Background(), config.Config{
		StripeSecretKey: "sk_test_123",
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "APP_BASE_URL") {
		t.Fatalf("error = %v, want missing APP_BASE_URL", err)
	}
}
