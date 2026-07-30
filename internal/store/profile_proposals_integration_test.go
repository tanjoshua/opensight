package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
)

// seedProposalBusiness inserts a plan, tenant, and draft business for proposal
// tests and registers cleanup. It returns the tenant and business ids.
func seedProposalBusiness(t *testing.T, ctx context.Context, db *pgxpool.Pool) (domain.ID, domain.ID) {
	t.Helper()
	tenantID := mustNewID(t)
	businessID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query131, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query132, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query133, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query134, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Proposal Tenant")
	if _, err := testdb.Exec(ctx, db, testdb.Query135, businessID, tenantID); err != nil {
		t.Fatalf("insert business: %v", err)
	}
	return tenantID, businessID
}

// TestProfileProposalStoreDiscardPending covers the regenerate discard half
// (ONB-4): an existing pending row is marked discarded and stops being pending,
// discarding with no pending row is a safe no-op, and a cross-tenant discard is
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

	tenantID, businessID := seedProposalBusiness(t, ctx, db)
	proposals := NewProfileProposalStore(db)
	payload := json.RawMessage(`{"low_confidence":false}`)

	// Discard with no pending row is a no-op (returns nil, does not error).
	if err := proposals.DiscardPending(ctx, tenantID, businessID); err != nil {
		t.Fatalf("discard with no pending row: %v", err)
	}

	if _, err := proposals.CreatePending(ctx, tenantID, businessID, payload); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	// Cross-tenant discard must not touch the row and must report ErrNotFound.
	otherTenant := mustNewID(t)
	if err := proposals.DiscardPending(ctx, otherTenant, businessID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant discard error = %v, want ErrNotFound", err)
	}
	if _, err := proposals.GetPending(ctx, tenantID, businessID); err != nil {
		t.Fatalf("pending row should survive cross-tenant discard: %v", err)
	}

	// Owning-tenant discard clears the pending row.
	if err := proposals.DiscardPending(ctx, tenantID, businessID); err != nil {
		t.Fatalf("discard pending: %v", err)
	}
	if _, err := proposals.GetPending(ctx, tenantID, businessID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get pending after discard = %v, want ErrNotFound", err)
	}

	// A fresh pending proposal can be created again (the partial unique index no
	// longer sees a pending row).
	if _, err := proposals.CreatePending(ctx, tenantID, businessID, payload); err != nil {
		t.Fatalf("create pending after discard: %v", err)
	}
}
