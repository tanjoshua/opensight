package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type CompetitorStatus string

const (
	CompetitorStatusDiscovered CompetitorStatus = "discovered"
	CompetitorStatusTracked    CompetitorStatus = "tracked"
	CompetitorStatusDismissed  CompetitorStatus = "dismissed"
)

type CompetitorRecord struct {
	ID               domain.ID
	BusinessID       domain.ID
	Name             string
	Website          *string
	Aliases          []string
	SuggestedAliases []string
	Source           string
	Status           CompetitorStatus
	CreatedAt        time.Time
}

type CreateManualCompetitorParams struct {
	ID         domain.ID
	AccountID   domain.ID
	BusinessID domain.ID
	Name       string
	Aliases    []string
	Website    *string
}

type SetCompetitorStatusParams struct {
	AccountID     domain.ID
	CompetitorID domain.ID
	Status       CompetitorStatus
}

type SuggestedAliasParams struct {
	AccountID     domain.ID
	CompetitorID domain.ID
	Alias        string
}

type UpdateCompetitorAliasesParams struct {
	AccountID     domain.ID
	CompetitorID domain.ID
	Aliases      []string
}

func (s *Store) CreateManual(ctx context.Context, params CreateManualCompetitorParams) (CompetitorRecord, error) {
	params, err := normalizeCreateManualCompetitorParams(params)
	if err != nil {
		return CompetitorRecord{}, err
	}

	row, err := s.q(ctx).CreateManualCompetitor(ctx, storesqlc.CreateManualCompetitorParams{
		ID: params.ID, BusinessID: params.BusinessID, AccountID: params.AccountID,
		Name: params.Name, Website: params.Website, Aliases: params.Aliases,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CompetitorRecord{}, ErrNotFound
		}
		return CompetitorRecord{}, fmt.Errorf("create manual competitor: %w", err)
	}
	return competitorFromSQLC(row), nil
}

func (s *Store) SetStatus(ctx context.Context, params SetCompetitorStatusParams) (CompetitorRecord, error) {
	if err := validateUUIDv7("account id", params.AccountID); err != nil {
		return CompetitorRecord{}, err
	}
	if err := validateUUIDv7("competitor id", params.CompetitorID); err != nil {
		return CompetitorRecord{}, err
	}
	if params.Status != CompetitorStatusTracked && params.Status != CompetitorStatusDismissed {
		return CompetitorRecord{}, errors.New("competitor status must be tracked or dismissed")
	}

	row, err := s.q(ctx).SetCompetitorStatus(ctx, storesqlc.SetCompetitorStatusParams{
		ID: params.CompetitorID, AccountID: params.AccountID, Status: string(params.Status),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CompetitorRecord{}, ErrNotFound
		}
		return CompetitorRecord{}, fmt.Errorf("set competitor status: %w", err)
	}
	return competitorFromSQLC(row), nil
}

func (s *Store) ApproveSuggestedAlias(ctx context.Context, params SuggestedAliasParams) (CompetitorRecord, error) {
	return s.updateSuggestedAlias(ctx, params, true)
}

func (s *Store) RejectSuggestedAlias(ctx context.Context, params SuggestedAliasParams) (CompetitorRecord, error) {
	return s.updateSuggestedAlias(ctx, params, false)
}

func (s *Store) UpdateAliases(ctx context.Context, params UpdateCompetitorAliasesParams) (CompetitorRecord, error) {
	if err := validateUUIDv7("account id", params.AccountID); err != nil {
		return CompetitorRecord{}, err
	}
	if err := validateUUIDv7("competitor id", params.CompetitorID); err != nil {
		return CompetitorRecord{}, err
	}
	params.Aliases = normalizeAliases(params.Aliases)
	row, err := s.q(ctx).UpdateCompetitorAliases(ctx, storesqlc.UpdateCompetitorAliasesParams{
		ID: params.CompetitorID, AccountID: params.AccountID, Aliases: params.Aliases,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CompetitorRecord{}, ErrNotFound
		}
		return CompetitorRecord{}, fmt.Errorf("update competitor aliases: %w", err)
	}
	return competitorFromSQLC(row), nil
}

func (s *Store) updateSuggestedAlias(ctx context.Context, params SuggestedAliasParams, approve bool) (CompetitorRecord, error) {
	params, err := normalizeSuggestedAliasParams(params)
	if err != nil {
		return CompetitorRecord{}, err
	}
	q := s.q(ctx)
	var row storesqlc.Competitor
	if approve {
		row, err = q.ApproveSuggestedAlias(ctx, storesqlc.ApproveSuggestedAliasParams{
			ID: params.CompetitorID, AccountID: params.AccountID, ArrayRemove: params.Alias,
		})
	} else {
		row, err = q.RejectSuggestedAlias(ctx, storesqlc.RejectSuggestedAliasParams{
			ID: params.CompetitorID, AccountID: params.AccountID, ArrayRemove: params.Alias,
		})
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CompetitorRecord{}, ErrNotFound
		}
		action := "reject"
		if approve {
			action = "approve"
		}
		return CompetitorRecord{}, fmt.Errorf("%s suggested competitor alias: %w", action, err)
	}
	return competitorFromSQLC(row), nil
}

func competitorFromSQLC(row storesqlc.Competitor) CompetitorRecord {
	return CompetitorRecord{
		ID: row.ID, BusinessID: row.BusinessID, Name: row.Name, Website: row.Website,
		Aliases: emptyStrings(row.Aliases), SuggestedAliases: emptyStrings(row.SuggestedAliases),
		Source: row.Source, Status: CompetitorStatus(row.Status), CreatedAt: row.CreatedAt,
	}
}

func normalizeSuggestedAliasParams(params SuggestedAliasParams) (SuggestedAliasParams, error) {
	if err := validateUUIDv7("account id", params.AccountID); err != nil {
		return SuggestedAliasParams{}, err
	}
	if err := validateUUIDv7("competitor id", params.CompetitorID); err != nil {
		return SuggestedAliasParams{}, err
	}
	params.Alias = strings.TrimSpace(params.Alias)
	if params.Alias == "" {
		return SuggestedAliasParams{}, errors.New("suggested alias is required")
	}
	return params, nil
}

func normalizeCreateManualCompetitorParams(params CreateManualCompetitorParams) (CreateManualCompetitorParams, error) {
	if params.ID == uuid.Nil {
		id, err := domain.NewID()
		if err != nil {
			return CreateManualCompetitorParams{}, err
		}
		params.ID = id
	}
	if err := validateUUIDv7("competitor id", params.ID); err != nil {
		return CreateManualCompetitorParams{}, err
	}
	if err := validateUUIDv7("account id", params.AccountID); err != nil {
		return CreateManualCompetitorParams{}, err
	}
	if err := validateUUIDv7("business id", params.BusinessID); err != nil {
		return CreateManualCompetitorParams{}, err
	}

	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return CreateManualCompetitorParams{}, errors.New("competitor name is required")
	}
	if params.Website != nil {
		website := strings.TrimSpace(*params.Website)
		if website == "" {
			params.Website = nil
		} else {
			params.Website = &website
		}
	}

	params.Aliases = normalizeAliases(params.Aliases)
	return params, nil
}

func normalizeAliases(values []string) []string {
	aliases := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		alias := strings.TrimSpace(raw)
		if alias == "" {
			continue
		}
		key := strings.ToLower(alias)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		aliases = append(aliases, alias)
	}
	return aliases
}
