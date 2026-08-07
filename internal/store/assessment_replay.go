package store

import (
	"context"
	"fmt"
	"time"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"
	"opensight/internal/visibility"
)

// ReplayFilter selects the stored generations `opensight assess replay` reads
// back. Every field is optional except MaxGenerations, which bounds the scan.
type ReplayFilter struct {
	BusinessID     *domain.ID
	Since, Until   *time.Time
	MaxGenerations int
}

// ReplayGeneration identifies one stored assessment generation. Replay derives
// the account from the row rather than taking it from the operator, so a
// business filter can never widen the account scope of what is read.
type ReplayGeneration struct {
	ID, AccountID, BusinessID, MonitoringRunID domain.ID
	Status                                     string
	StartedAt                                  time.Time
}

// StoredAssessment is the verdict a generation actually recorded, which replay
// compares against the verdict today's assessors derive from the same evidence.
type StoredAssessment struct {
	PracticeKey, SubjectKey string
	Status                  visibility.AssessmentStatus
}

func (s *Store) ListReplayGenerations(ctx context.Context, filter ReplayFilter) ([]ReplayGeneration, error) {
	rows, err := s.q(ctx).ListReplayGenerations(ctx, storesqlc.ListReplayGenerationsParams{
		BusinessID:     filter.BusinessID,
		Since:          filter.Since,
		Until:          filter.Until,
		MaxGenerations: int32(filter.MaxGenerations),
	})
	if err != nil {
		return nil, fmt.Errorf("list replay generations: %w", err)
	}
	out := make([]ReplayGeneration, 0, len(rows))
	for _, r := range rows {
		out = append(out, ReplayGeneration{ID: r.ID, AccountID: r.AccountID, BusinessID: r.BusinessID, MonitoringRunID: r.MonitoringRunID, Status: r.Status, StartedAt: r.StartedAt})
	}
	return out, nil
}

// LoadGenerationEvidence returns the successfully collected artifacts of one
// generation, carrying their original payload_version so an assessor that no
// longer understands an old version fails on decode instead of guessing.
func (s *Store) LoadGenerationEvidence(ctx context.Context, generationID domain.ID) ([]visibility.EvidenceArtifact, error) {
	rows, err := s.q(ctx).ListGenerationEvidence(ctx, generationID)
	if err != nil {
		return nil, fmt.Errorf("load generation evidence: %w", err)
	}
	out := make([]visibility.EvidenceArtifact, 0, len(rows))
	for _, r := range rows {
		out = append(out, visibility.EvidenceArtifact{CollectorKey: r.CollectorKey, CollectorVersion: int(r.CollectorVersion), PayloadVersion: int(r.PayloadVersion), CheckedAt: r.CheckedAt, Payload: r.Payload})
	}
	return out, nil
}

func (s *Store) ListGenerationAssessments(ctx context.Context, generationID domain.ID) ([]StoredAssessment, error) {
	rows, err := s.q(ctx).ListGenerationAssessments(ctx, generationID)
	if err != nil {
		return nil, fmt.Errorf("list generation assessments: %w", err)
	}
	out := make([]StoredAssessment, 0, len(rows))
	for _, r := range rows {
		out = append(out, StoredAssessment{PracticeKey: r.PracticeKey, SubjectKey: r.SubjectKey, Status: visibility.AssessmentStatus(r.Status)})
	}
	return out, nil
}
