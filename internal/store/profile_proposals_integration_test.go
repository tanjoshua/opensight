package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedProposalBusiness inserts a plan, account, and draft business for proposal
// tests and registers cleanup. It returns the account and business ids.
func seedProposalBusiness(t *testing.T, ctx context.Context, db *pgxpool.Pool) (domain.ID, domain.ID) {
	t.Helper()
	accountID := mustNewID(t)
	businessID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM profile_proposals WHERE business_id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Proposal Account")
	if _, err := db.Exec(ctx, "INSERT INTO businesses (id, account_id, status, name) VALUES ($1, $2, 'draft', 'Proposal Clinic')", businessID, accountID); err != nil {
		t.Fatalf("insert business: %v", err)
	}
	return accountID, businessID
}

// TestProfileProposalStoreDiscardPending covers the regenerate discard half
// (ONB-4): an existing pending row is marked discarded and stops being pending,
// discarding with no pending row is a safe no-op, and a cross-account discard is
// refused as ErrNotFound.
func TestProfileProposalStoreDiscardPending(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	accountID, businessID := seedProposalBusiness(t, ctx, db)
	proposals := New(db)
	payload := json.RawMessage(`{"low_confidence":false}`)

	// Discard with no pending row is a no-op (returns nil, does not error).
	if err := proposals.DiscardPending(ctx, accountID, businessID); err != nil {
		t.Fatalf("discard with no pending row: %v", err)
	}

	if _, err := proposals.CreatePending(ctx, accountID, businessID, payload); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	// Cross-account discard must not touch the row and must report ErrNotFound.
	otherAccount := mustNewID(t)
	if err := proposals.DiscardPending(ctx, otherAccount, businessID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account discard error = %v, want ErrNotFound", err)
	}
	if _, err := proposals.GetPending(ctx, accountID, businessID); err != nil {
		t.Fatalf("pending row should survive cross-account discard: %v", err)
	}

	// Owning-account discard clears the pending row.
	if err := proposals.DiscardPending(ctx, accountID, businessID); err != nil {
		t.Fatalf("discard pending: %v", err)
	}
	if _, err := proposals.GetPending(ctx, accountID, businessID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get pending after discard = %v, want ErrNotFound", err)
	}

	// A fresh pending proposal can be created again (the partial unique index no
	// longer sees a pending row).
	if _, err := proposals.CreatePending(ctx, accountID, businessID, payload); err != nil {
		t.Fatalf("create pending after discard: %v", err)
	}
}
