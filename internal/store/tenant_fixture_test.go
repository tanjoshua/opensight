package store

import (
	"context"
	"testing"

	"opensight/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
)

// insertTenant inserts a tenant row plus its comped starter subscription — the
// fixture every store integration test needs for a valid tenant now that
// entitlements live in the billing catalog rather than a seeded plans row
// (BILL-1). Most tests don't exercise billing logic; comped=true on the
// starter plan is a valid tenant for all of them.
func insertTenant(t *testing.T, db *pgxpool.Pool, ctx context.Context, tenantID domain.ID, name string) {
	t.Helper()
	mustExec(t, db, ctx, testdb.Query225, tenantID, name)
	mustExec(t, db, ctx, testdb.Query226, tenantID)
}

func mustUUIDV7(t *testing.T, value string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse UUID: %v", err)
	}
	return id
}
