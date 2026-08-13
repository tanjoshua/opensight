package store

import (
	"context"
	"errors"
	"fmt"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

const (
	GenerationStatusGenerating  = "generating"
	GenerationStatusReady       = "ready"
	GenerationStatusFailed      = "failed"
	GenerationStageFetchingSite = "fetching_site"
	GenerationStageDrafting     = "drafting"
)

// InstallGeneration fences all writes from earlier onboarding jobs. Callers
// invoke it in the same transaction that inserts the River job.
func (s *Store) InstallGeneration(ctx context.Context, tx pgx.Tx, accountID, businessID, generationID domain.ID, jobID int64, website *string) error {
	if jobID < 1 {
		return errors.New("generation job id must be positive")
	}
	_, err := storesqlc.New(tx).InstallBusinessGeneration(ctx, storesqlc.InstallBusinessGenerationParams{
		BusinessID: businessID, AccountID: accountID, Website: website,
		GenerationID: &generationID, GenerationJobID: &jobID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("install business generation: %w", err)
	}
	return nil
}

func (s *Store) UpdateGenerationStage(ctx context.Context, businessID, generationID domain.ID, stage string) (bool, error) {
	n, err := s.q(ctx).UpdateBusinessGenerationStage(ctx, storesqlc.UpdateBusinessGenerationStageParams{
		BusinessID: businessID, GenerationID: &generationID, Stage: &stage,
	})
	return n > 0, err
}

func (s *Store) FinishGeneration(ctx context.Context, businessID, generationID domain.ID, status string) (bool, error) {
	if status != GenerationStatusReady && status != GenerationStatusFailed {
		return false, fmt.Errorf("invalid terminal generation status %q", status)
	}
	n, err := s.q(ctx).FinishBusinessGeneration(ctx, storesqlc.FinishBusinessGenerationParams{
		BusinessID: businessID, GenerationID: &generationID, Status: &status,
	})
	return n > 0, err
}
