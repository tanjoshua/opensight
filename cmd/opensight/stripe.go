package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"opensight/internal/billing"
	billingstripe "opensight/internal/billing/stripe"
	"opensight/internal/config"
)

// stripePortalConfigUsage documents `opensight stripe portal-config` (design 08
// "Customer Portal"): applies internal/billing.DesiredPortalConfig to the
// explicitly provisioned STRIPE_PORTAL_CONFIGURATION_ID.
const stripePortalConfigUsage = "usage: opensight stripe portal-config"

// stripeWebhookConfigUsage documents `opensight stripe webhook-config`
// (design 08 "Webhook"): idempotently creates or updates the production
// webhook endpoint at APP_BASE_URL + /webhooks/stripe with
// internal/billing.DesiredWebhookEvents enabled.
const stripeWebhookConfigUsage = "usage: opensight stripe webhook-config"

const stripeUsage = "usage: opensight stripe <portal-config|webhook-config>"

func runStripeCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("stripe subcommand required; %s", stripeUsage)
	}
	switch args[0] {
	case "portal-config":
		if len(args) > 1 {
			return fmt.Errorf("unexpected argument %q; %s", args[1], stripePortalConfigUsage)
		}
		return applyPortalConfigCLI(ctx, cfg, os.Stdout)
	case "webhook-config":
		if len(args) > 1 {
			return fmt.Errorf("unexpected argument %q; %s", args[1], stripeWebhookConfigUsage)
		}
		return applyWebhookConfigCLI(ctx, cfg, os.Stdout)
	default:
		return fmt.Errorf("unknown stripe subcommand %q; %s", args[0], stripeUsage)
	}
}

// applyPortalConfigCLI requires only the settings this operator command uses.
func applyPortalConfigCLI(ctx context.Context, cfg config.Config, out io.Writer) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cfg.StripeSecretKey == "" {
		return fmt.Errorf("STRIPE_SECRET_KEY is required to apply the portal configuration")
	}
	if cfg.AppBaseURL == "" {
		return fmt.Errorf("APP_BASE_URL is required to apply the portal configuration")
	}
	if cfg.StripePortalConfigurationID == "" {
		return fmt.Errorf("STRIPE_PORTAL_CONFIGURATION_ID is required to apply the portal configuration")
	}

	provider, err := billingstripe.New(billingstripe.Config{APIKey: cfg.StripeSecretKey})
	if err != nil {
		return err
	}

	desired := billing.DesiredPortalConfig
	desired.DefaultReturnURL = cfg.AppBaseURL + "/billing"

	if err := provider.ApplyPortalConfiguration(ctx, cfg.StripePortalConfigurationID, desired); err != nil {
		return fmt.Errorf("apply portal configuration: %w", err)
	}

	return writePortalConfigResult(out, cfg.StripePortalConfigurationID)
}

func writePortalConfigResult(out io.Writer, id string) error {
	_, err := fmt.Fprintf(out, "STRIPE_PORTAL_CONFIGURATION_ID=%s\naction=updated\n", id)
	return err
}

// applyWebhookConfigCLI requires only the settings this operator command uses.
func applyWebhookConfigCLI(ctx context.Context, cfg config.Config, out io.Writer) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cfg.StripeSecretKey == "" {
		return fmt.Errorf("STRIPE_SECRET_KEY is required to apply the webhook endpoint")
	}
	if cfg.AppBaseURL == "" {
		return fmt.Errorf("APP_BASE_URL is required to apply the webhook endpoint")
	}

	provider, err := billingstripe.New(billingstripe.Config{APIKey: cfg.StripeSecretKey})
	if err != nil {
		return err
	}

	result, err := provider.ApplyWebhookEndpoint(ctx, cfg.AppBaseURL+"/webhooks/stripe", billing.DesiredWebhookEvents)
	if err != nil {
		return fmt.Errorf("apply webhook endpoint: %w", err)
	}

	return writeWebhookConfigResult(out, result)
}

// writeWebhookConfigResult prints STRIPE_WEBHOOK_SECRET only when the
// endpoint was just created — Stripe never returns it again after that, so
// this is the one moment an operator can capture it into secrets.sops.yml.
// On an update, the previously stored secret is still correct and unchanged.
func writeWebhookConfigResult(out io.Writer, result billingstripe.WebhookEndpointResult) error {
	if result.Created {
		_, err := fmt.Fprintf(out, "id=%s\naction=created\nSTRIPE_WEBHOOK_SECRET=%s\n", result.ID, result.Secret)
		return err
	}
	_, err := fmt.Fprintf(out, "id=%s\naction=updated\n", result.ID)
	return err
}
