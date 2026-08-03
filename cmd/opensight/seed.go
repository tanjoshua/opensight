package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"opensight/internal/config"
	"opensight/internal/store"

	"github.com/google/uuid"
)

// Fixed UUIDv7-form IDs make `opensight seed dev` idempotent.
var (
	seedTenantID = uuid.MustParse("01950000-0000-7000-8000-0000000000d0")
	seedUserID   = uuid.MustParse("01950000-0000-7000-8000-0000000000d1")
)

const seedTenantName = "Local Dev Tenant"

// seedDevArgsUsage documents seed dev's one flag.
const seedDevArgsUsage = "usage: opensight seed dev --email <your google account email>"

// parseSeedDevArgs resolves the email to seed: --email, falling back to
// OPENSIGHT_DEV_EMAIL so `make seed-dev` can stay a bare command once that's
// set in the developer's shell.
func parseSeedDevArgs(args []string, getenv func(string) string) (string, error) {
	flags := flag.NewFlagSet("seed dev", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "google account email to seed as the dev tenant's user")
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("%s", seedDevArgsUsage)
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("unexpected argument %q; %s", flags.Arg(0), seedDevArgsUsage)
	}

	value := strings.TrimSpace(*email)
	if value == "" {
		value = strings.TrimSpace(getenv("OPENSIGHT_DEV_EMAIL"))
	}
	if value == "" {
		return "", fmt.Errorf("--email or OPENSIGHT_DEV_EMAIL is required; %s", seedDevArgsUsage)
	}
	return value, nil
}

// seedDevCLI creates a comped tenant and a user row for email, with no Google
// identity yet, so local development can sign in with a real Google account
// and land straight in the app instead of hitting the billing wall (design 07
// "Auth and accounts"; design 08 "Signup"). The first Google sign-in with
// that address links it.
func seedDevCLI(ctx context.Context, cfg config.Config, email string, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}

	db, err := store.Open(ctx, cfg.DatabaseURL, int32(cfg.DBMaxOpenConns))
	if err != nil {
		return err
	}
	defer db.Close()

	dataStore := store.New(db)
	if _, err := dataStore.GetUserByEmail(ctx, email); err == nil {
		_, err = fmt.Fprintln(out, "dev account already seeded; nothing to do")
		return err
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	// If a previous attempt stopped after creating the tenant, reuse it and
	// finish creating the account.
	if _, err := dataStore.GetByTenant(ctx, seedTenantID); errors.Is(err, store.ErrNotFound) {
		if _, err := dataStore.CreateTenant(ctx, store.CreateTenantParams{ID: seedTenantID, Name: seedTenantName}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if _, err := dataStore.CreateUser(ctx, store.CreateUserParams{
		ID:       seedUserID,
		TenantID: seedTenantID,
		Email:    email,
	}); err != nil {
		return err
	}

	_, err = fmt.Fprintf(out,
		"seeded dev account\ntenant_id=%s\nuser_email=%s\nsign in with Google using this address\n",
		seedTenantID, email,
	)
	return err
}
