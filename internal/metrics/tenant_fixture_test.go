package metrics

import (
	"context"
	"database/sql"
	"testing"

	"opensight/internal/domain"
)

// insertTenant inserts a tenant row plus its comped starter subscription — the
// fixture every metrics integration test needs for a valid tenant now that
// entitlements live in the billing catalog rather than a seeded plans row
// (BILL-1). These tests exercise metrics queries, not billing logic;
// comped=true on the starter plan is a valid tenant for all of them.
func insertTenant(t *testing.T, db *sql.DB, ctx context.Context, tenantID domain.ID, name string) {
	t.Helper()
	mustExec(t, db, ctx, `INSERT INTO tenants (id, name) VALUES ($1, $2)`, tenantID, name)
	mustExec(t, db, ctx, `INSERT INTO subscriptions (tenant_id, plan_code, comped) VALUES ($1, 'starter', true)`, tenantID)
}
