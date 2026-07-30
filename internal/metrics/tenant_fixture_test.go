package metrics

import (
	"context"
	"testing"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
)

// insertTenant inserts a tenant row plus its comped starter subscription — the
// fixture every metrics integration test needs for a valid tenant now that
// entitlements live in the billing catalog rather than a seeded plans row
// (BILL-1). These tests exercise metrics queries, not billing logic;
// comped=true on the starter plan is a valid tenant for all of them.
func insertTenant(t *testing.T, db *pgxpool.Pool, ctx context.Context, tenantID domain.ID, name string) {
	t.Helper()
	mustExec(t, db, ctx, testdb.Query049, tenantID, name)
	mustExec(t, db, ctx, testdb.Query050, tenantID)
}
