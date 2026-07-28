package workflows

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"opensight/internal/llm"
	"opensight/internal/store"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPersistProposalIdempotent proves PersistProposal survives Temporal's
// at-least-once activity execution: a second call for the same business (as
// happens when a first attempt committed but its ack was lost) reuses the
// existing pending row rather than erroring on the one-pending-per-business
// unique index.
func TestPersistProposalIdempotent(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run activity integration tests")
	}

	ctx := context.Background()
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenantID := mustID(t)
	businessID := mustID(t)
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM profile_proposals WHERE business_id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.ExecContext(ctx, "DELETE FROM subscriptions WHERE tenant_id = $1", tenantID)
		_, _ = db.ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Persist Tenant")
	mustExec(t, db, ctx,
		`INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Persist Clinic')`,
		businessID, tenantID)

	acts := NewActivities(nil, nil, nil, nil, nil, nil, nil, nil, nil, store.NewProfileProposalStore(db))
	in := PersistProposalInput{
		TenantID:   tenantID,
		BusinessID: businessID,
		Payload:    llm.ProposalPayload{LowConfidence: true},
	}

	first, err := acts.PersistProposal(ctx, in)
	if err != nil {
		t.Fatalf("first persist: %v", err)
	}
	second, err := acts.PersistProposal(ctx, in)
	if err != nil {
		t.Fatalf("second persist (idempotent retry): %v", err)
	}
	if first.ProposalID != second.ProposalID {
		t.Fatalf("idempotent persist returned different ids: %s vs %s", first.ProposalID, second.ProposalID)
	}
}
