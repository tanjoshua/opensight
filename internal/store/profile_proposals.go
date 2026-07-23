package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrPendingProposalExists is returned when a pending profile proposal already
// exists for the business (the partial-unique index allows only one).
var ErrPendingProposalExists = errors.New("pending profile proposal already exists for business")

const pendingProposalConstraint = "profile_proposals_one_pending_per_business_idx"

// ProfileProposalStatus is the persisted status of a profile proposal.
type ProfileProposalStatus string

const (
	// ProfileProposalStatusPending is an unresolved proposal.
	ProfileProposalStatusPending ProfileProposalStatus = "pending"
	// ProfileProposalStatusApplied is a proposal whose changes were accepted.
	ProfileProposalStatusApplied ProfileProposalStatus = "applied"
	// ProfileProposalStatusDiscarded is a rejected proposal.
	ProfileProposalStatusDiscarded ProfileProposalStatus = "discarded"
)

// ProfileProposal is a persisted profile_proposals row (migration 00003).
type ProfileProposal struct {
	ID         domain.ID
	BusinessID domain.ID
	Payload    json.RawMessage
	Status     ProfileProposalStatus
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

// ProfileProposalStore reads and writes profile_proposals rows. It is the thin
// version needed by Phase-1 callers; epic 10 owns the apply/discard state
// machine.
type ProfileProposalStore struct {
	db *sql.DB
}

// NewProfileProposalStore returns a ProfileProposalStore backed by db.
func NewProfileProposalStore(db *sql.DB) *ProfileProposalStore {
	return &ProfileProposalStore{db: db}
}

const (
	proposalColumns = `id, business_id, payload, status, created_at, resolved_at`

	insertPendingProposalSQL = `
INSERT INTO profile_proposals (id, business_id, payload, status)
VALUES ($1, $2, $3::jsonb, 'pending')
RETURNING created_at`

	getPendingProposalSQL = `
SELECT ` + proposalColumns + `
FROM profile_proposals
WHERE business_id = $1 AND status = 'pending'`

	discardPendingProposalSQL = `
UPDATE profile_proposals
SET status = 'discarded', resolved_at = now()
WHERE business_id = $1 AND status = 'pending'`
)

// CreatePending inserts a pending proposal for the business, entering through
// the tenant-checked business lookup in the same transaction as the write. A
// missing or cross-tenant business returns ErrNotFound; an existing pending
// proposal returns ErrPendingProposalExists.
func (s *ProfileProposalStore) CreatePending(ctx context.Context, tenantID, businessID domain.ID, payload json.RawMessage) (ProfileProposal, error) {
	if s == nil || s.db == nil {
		return ProfileProposal{}, errors.New("profile proposal store database is required")
	}
	if len(payload) == 0 {
		return ProfileProposal{}, errors.New("proposal payload is required")
	}

	id, err := domain.NewID()
	if err != nil {
		return ProfileProposal{}, err
	}

	proposal := ProfileProposal{
		ID:         id,
		BusinessID: businessID,
		Payload:    payload,
		Status:     ProfileProposalStatusPending,
	}
	err = withTx(ctx, s.db, func(q querier) error {
		if err := businessOwned(ctx, q, tenantID, businessID); err != nil {
			return err
		}
		if err := q.queryRowContext(
			ctx,
			insertPendingProposalSQL,
			id,
			businessID,
			string(payload),
		).Scan(&proposal.CreatedAt); err != nil {
			if isConstraintViolation(err, pendingProposalConstraint) {
				return ErrPendingProposalExists
			}
			return fmt.Errorf("insert pending proposal: %w", err)
		}
		return nil
	})
	if err != nil {
		return ProfileProposal{}, err
	}
	return proposal, nil
}

// GetPending returns the business's pending proposal. It enters through the
// tenant-checked business lookup; a missing or cross-tenant business, or a
// business with no pending proposal, returns ErrNotFound.
func (s *ProfileProposalStore) GetPending(ctx context.Context, tenantID, businessID domain.ID) (ProfileProposal, error) {
	if s == nil || s.db == nil {
		return ProfileProposal{}, errors.New("profile proposal store database is required")
	}

	q := sqlQuerier{q: s.db}
	if err := businessOwned(ctx, q, tenantID, businessID); err != nil {
		return ProfileProposal{}, err
	}

	var proposal ProfileProposal
	if err := q.queryRowContext(ctx, getPendingProposalSQL, businessID).Scan(
		&proposal.ID,
		&proposal.BusinessID,
		&proposal.Payload,
		&proposal.Status,
		&proposal.CreatedAt,
		&proposal.ResolvedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProfileProposal{}, ErrNotFound
		}
		return ProfileProposal{}, fmt.Errorf("get pending proposal: %w", err)
	}
	return proposal, nil
}

// DiscardPending marks the business's pending proposal (if any) discarded. It is
// the discard half of regenerate (ONB-4): a safe no-op when there is no pending
// row, so the caller can always call it before starting a fresh generation. It
// enters through the tenant-checked business lookup in the same transaction as
// the update; a missing or cross-tenant business returns ErrNotFound.
func (s *ProfileProposalStore) DiscardPending(ctx context.Context, tenantID, businessID domain.ID) error {
	if s == nil || s.db == nil {
		return errors.New("profile proposal store database is required")
	}
	return withTx(ctx, s.db, func(q querier) error {
		if err := businessOwned(ctx, q, tenantID, businessID); err != nil {
			return err
		}
		if _, err := q.execContext(ctx, discardPendingProposalSQL, businessID); err != nil {
			return fmt.Errorf("discard pending proposal: %w", err)
		}
		return nil
	})
}

func isConstraintViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
