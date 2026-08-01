package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
