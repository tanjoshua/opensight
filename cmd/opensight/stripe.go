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

func runStripeCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("stripe subcommand required; %s", stripePortalConfigUsage)
	}
	switch args[0] {
	case "portal-config":
		if len(args) > 1 {
			return fmt.Errorf("unexpected argument %q; %s", args[1], stripePortalConfigUsage)
		}
		return applyPortalConfigCLI(ctx, cfg, os.Stdout)
	default:
		return fmt.Errorf("unknown stripe subcommand %q; %s", args[0], stripePortalConfigUsage)
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
