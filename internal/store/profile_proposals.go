package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
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

// CreatePending inserts a pending proposal for the business, entering through
// the account-checked business lookup in the same transaction as the write. A
// missing or cross-account business returns ErrNotFound; an existing pending
// proposal returns ErrPendingProposalExists.
func (s *Store) CreatePending(ctx context.Context, accountID, businessID domain.ID, payload json.RawMessage) (ProfileProposal, error) {
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
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		if err := businessOwned(ctx, q, accountID, businessID); err != nil {
			return err
		}
		proposal.CreatedAt, err = q.InsertPendingProposal(ctx, storesqlc.InsertPendingProposalParams{
			ID: id, BusinessID: businessID, Payload: payload,
		})
		if err != nil {
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

// CreatePendingFenced persists only when generationID is still installed on
// the draft business. A superseded job returns (zero, false, nil).
func (s *Store) CreatePendingFenced(ctx context.Context, accountID, businessID, generationID domain.ID, payload json.RawMessage) (ProfileProposal, bool, error) {
	if len(payload) == 0 {
		return ProfileProposal{}, false, errors.New("proposal payload is required")
	}
	id, err := domain.NewID()
	if err != nil {
		return ProfileProposal{}, false, err
	}
	proposal := ProfileProposal{ID: id, BusinessID: businessID, Payload: payload, Status: ProfileProposalStatusPending}
	current := false
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		if err := businessOwned(ctx, q, accountID, businessID); err != nil {
			return err
		}
		ok, err := q.GenerationIsCurrent(ctx, storesqlc.GenerationIsCurrentParams{BusinessID: businessID, GenerationID: &generationID})
		if err != nil {
			return fmt.Errorf("check generation fence: %w", err)
		}
		if !ok {
			return nil
		}
		current = true
		proposal.CreatedAt, err = q.InsertPendingProposal(ctx, storesqlc.InsertPendingProposalParams{ID: id, BusinessID: businessID, Payload: payload})
		if err != nil && isConstraintViolation(err, pendingProposalConstraint) {
			return ErrPendingProposalExists
		}
		return err
	})
	if err != nil {
		return ProfileProposal{}, false, err
	}
	return proposal, current, nil
}

// GetPending returns the business's pending proposal. It enters through the
// account-checked business lookup; a missing or cross-account business, or a
// business with no pending proposal, returns ErrNotFound.
func (s *Store) GetPending(ctx context.Context, accountID, businessID domain.ID) (ProfileProposal, error) {

	q := s.q(ctx)
	if err := businessOwned(ctx, q, accountID, businessID); err != nil {
		return ProfileProposal{}, err
	}

	row, err := q.GetPendingProposal(ctx, businessID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProfileProposal{}, ErrNotFound
		}
		return ProfileProposal{}, fmt.Errorf("get pending proposal: %w", err)
	}
	return ProfileProposal{
		ID: row.ID, BusinessID: row.BusinessID, Payload: row.Payload,
		Status: ProfileProposalStatus(row.Status), CreatedAt: row.CreatedAt, ResolvedAt: row.ResolvedAt,
	}, nil
}

// DiscardPending marks the business's pending proposal (if any) discarded. It is
// the discard half of regenerate: a safe no-op when there is no pending
// row, so the caller can always call it before starting a fresh generation. It
// enters through the account-checked business lookup in the same transaction as
// the update; a missing or cross-account business returns ErrNotFound.
func (s *Store) DiscardPending(ctx context.Context, accountID, businessID domain.ID) error {
	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		return discardPending(ctx, q, accountID, businessID)
	})
}

// DiscardPendingTx is DiscardPending on a caller-owned transaction. It lets
// regeneration discard the old proposal in the same commit that installs the
// replacement generation token and River job.
func (s *Store) DiscardPendingTx(ctx context.Context, tx pgx.Tx, accountID, businessID domain.ID) error {
	return discardPending(ctx, storesqlc.New(tx), accountID, businessID)
}

func discardPending(ctx context.Context, q *storesqlc.Queries, accountID, businessID domain.ID) error {
	if err := businessOwned(ctx, q, accountID, businessID); err != nil {
		return err
	}
	if err := q.DiscardPendingProposal(ctx, businessID); err != nil {
		return fmt.Errorf("discard pending proposal: %w", err)
	}
	return nil
}

func isConstraintViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
