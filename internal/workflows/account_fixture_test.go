package workflows

import (
	"context"
	"testing"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// insertAccount inserts a account row plus its comped starter subscription — the
// fixture every workflows integration test needs for a valid account now that
// entitlements live in the billing catalog rather than a seeded plans row
// (BILL-1). These tests exercise workflow activities, not billing logic;
// comped=true on the starter plan is a valid account for all of them.
func insertAccount(t *testing.T, db *pgxpool.Pool, ctx context.Context, accountID domain.ID, name string) {
	t.Helper()
	mustExec(t, db, ctx, "INSERT INTO accounts (id, name, slug) VALUES ($1, $2, $3)", accountID, name, "test-"+accountID.String())
	mustExec(t, db, ctx, "INSERT INTO subscriptions (account_id, plan_code, comped) VALUES ($1, 'starter', true)", accountID)
}
