package store

import (
	"context"
	"testing"

	"opensight/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// insertAccount inserts an account row plus its comped starter subscription.
// entitlements live in the billing catalog rather than a seeded plans row
// (BILL-1). Most tests don't exercise billing logic; comped=true on the
// starter plan is a valid account for all of them.
func insertAccount(t *testing.T, db *pgxpool.Pool, ctx context.Context, accountID domain.ID, name string) {
	t.Helper()
	mustExec(t, db, ctx, "INSERT INTO accounts (id, name, slug) VALUES ($1, $2, $3)", accountID, name, accountSlug(name, accountID))
	mustExec(t, db, ctx, "INSERT INTO subscriptions (account_id, plan_code, comped) VALUES ($1, 'starter', true)", accountID)
}

func mustUUIDV7(t *testing.T, value string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse UUID: %v", err)
	}
	return id
}
