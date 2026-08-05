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
	seedAccountID = uuid.MustParse("01950000-0000-7000-8000-0000000000d0")
)

const seedAccountName = "Local Dev Account"

// seedDevArgsUsage documents seed dev's one flag.
const seedDevArgsUsage = "usage: opensight seed dev --email <your google account email>"

// parseSeedDevArgs resolves the email to seed: --email, falling back to
// OPENSIGHT_DEV_EMAIL so `make seed-dev` can stay a bare command once that's
// set in the developer's shell.
func parseSeedDevArgs(args []string, getenv func(string) string) (string, error) {
	flags := flag.NewFlagSet("seed dev", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "google account email to seed as the dev account's owner")
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

// seedDevCLI creates a comped account and an owner membership for email, with no Google
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
	// If a previous attempt stopped after creating the account, reuse it and
	// finish creating the owner membership.
	if _, err := dataStore.GetByAccount(ctx, seedAccountID); errors.Is(err, store.ErrNotFound) {
		if _, err := dataStore.CreateOperatorAccount(ctx, store.CreateOperatorAccountParams{ID: seedAccountID, Name: seedAccountName}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if _, err := dataStore.AddAccountMember(ctx, store.AddAccountMemberParams{
		AccountID: seedAccountID,
		Email:     email,
		Role:      store.AccountRoleOwner,
	}); err != nil {
		return err
	}

	_, err = fmt.Fprintf(out,
		"seeded dev account\naccount_id=%s\nuser_email=%s\nsign in with Google using this address\n",
		seedAccountID, email,
	)
	return err
}
