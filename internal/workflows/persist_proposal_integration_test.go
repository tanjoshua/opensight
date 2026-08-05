package workflows

import (
	"context"
	"os"
	"testing"

	"opensight/internal/llm"
	"opensight/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
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

	accountID := mustID(t)
	businessID := mustID(t)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM profile_proposals WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Persist Account")
	mustExec(t, db, ctx, "INSERT INTO businesses (id, account_id, status, name) VALUES ($1, $2, 'draft', 'Persist Clinic')", businessID, accountID)

	acts := &Activities{Store: store.New(db)}
	in := PersistProposalInput{
		AccountID:   accountID,
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
