package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"opensight/internal/auth"
	"opensight/internal/config"
	"opensight/internal/store"

	"github.com/google/uuid"
)

// Fixed UUIDv7-form IDs make `opensight seed dev` idempotent.
var (
	seedTenantID = uuid.MustParse("01950000-0000-7000-8000-0000000000d0")
	seedUserID   = uuid.MustParse("01950000-0000-7000-8000-0000000000d1")
)

const (
	seedTenantName = "Local Dev Tenant"
	seedEmail      = "dev@opensight.local"
	seedPassword   = "opensight-dev"
)

// seedDevCLI creates only a tenant and user so local development can exercise
// the complete onboarding flow after login.
func seedDevCLI(ctx context.Context, cfg config.Config, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}

	db, err := store.Open(cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	admin := store.NewAdminStore(db)
	authStore := store.NewAuthStore(db)
	if _, err := authStore.GetUserCredentials(ctx, seedEmail); err == nil {
		_, err = fmt.Fprintln(out, "dev account already seeded; nothing to do")
		return err
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	// If a previous attempt stopped after creating the tenant, reuse it and
	// finish creating the account.
	if _, err := admin.GetTenantPlan(ctx, seedTenantID); errors.Is(err, store.ErrNotFound) {
		if _, err := admin.CreateTenant(ctx, store.CreateTenantParams{ID: seedTenantID, Name: seedTenantName}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	passwordHash, err := auth.HashPassword(seedPassword)
	if err != nil {
		return err
	}
	if _, err := admin.CreateUser(ctx, store.CreateUserParams{
		ID:           seedUserID,
		TenantID:     seedTenantID,
		Email:        seedEmail,
		PasswordHash: passwordHash,
	}); err != nil {
		return err
	}

	_, err = fmt.Fprintf(out,
		"seeded dev account\ntenant_id=%s\nuser_email=%s\npassword=%s\n",
		seedTenantID, seedEmail, seedPassword,
	)
	return err
}
