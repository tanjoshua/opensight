package workflows

import (
	"context"
	"os"
	"testing"

	"opensight/internal/llm"
	"opensight/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
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
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	tenantID := mustID(t)
	businessID := mustID(t)
	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query255, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query256, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query257, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query258, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Persist Tenant")
	mustExec(t, db, ctx, testdb.Query259, businessID, tenantID)

	acts := &Activities{Proposals: store.NewProfileProposalStore(db)}
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
