package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/domain"

	"github.com/google/uuid"
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
	TenantID   domain.ID
	BusinessID domain.ID
	Name       string
	Aliases    []string
	Website    *string
}

type SetCompetitorStatusParams struct {
	TenantID     domain.ID
	CompetitorID domain.ID
	Status       CompetitorStatus
}

type SuggestedAliasParams struct {
	TenantID     domain.ID
	CompetitorID domain.ID
	Alias        string
}

type UpdateCompetitorAliasesParams struct {
	TenantID     domain.ID
	CompetitorID domain.ID
	Aliases      []string
}

type CompetitorStore struct {
	db *sql.DB
}

func NewCompetitorStore(db *sql.DB) *CompetitorStore {
	return &CompetitorStore{db: db}
}

const competitorRecordColumns = `co.id, co.business_id, co.name, co.website,
       to_jsonb(co.aliases), to_jsonb(co.suggested_aliases),
       co.source, co.status, co.created_at`

const createManualCompetitorSQL = `
INSERT INTO competitors (id, business_id, name, website, aliases, source, status)
SELECT $1, b.id, $4, $5, $6, 'manual', 'tracked'
FROM businesses b
WHERE b.id = $2 AND b.tenant_id = $3
RETURNING id, business_id, name, website, to_jsonb(aliases),
          to_jsonb(suggested_aliases), source, status, created_at`

const setCompetitorStatusSQL = `
UPDATE competitors co
SET status = $3
FROM businesses b
WHERE co.id = $1
  AND co.business_id = b.id
  AND b.tenant_id = $2
RETURNING ` + competitorRecordColumns

const approveSuggestedAliasSQL = `
UPDATE competitors co
SET suggested_aliases = array_remove(co.suggested_aliases, $3),
    aliases = CASE
      WHEN $3 = ANY(co.aliases) THEN co.aliases
      ELSE array_append(co.aliases, $3)
    END
FROM businesses b
WHERE co.id = $1
  AND co.business_id = b.id
  AND b.tenant_id = $2
  AND $3 = ANY(co.suggested_aliases)
RETURNING ` + competitorRecordColumns

const rejectSuggestedAliasSQL = `
UPDATE competitors co
SET suggested_aliases = array_remove(co.suggested_aliases, $3)
FROM businesses b
WHERE co.id = $1
  AND co.business_id = b.id
  AND b.tenant_id = $2
  AND $3 = ANY(co.suggested_aliases)
RETURNING ` + competitorRecordColumns

const updateCompetitorAliasesSQL = `
UPDATE competitors co
SET aliases = $3
FROM businesses b
WHERE co.id = $1
  AND co.business_id = b.id
  AND b.tenant_id = $2
RETURNING ` + competitorRecordColumns

func (s *CompetitorStore) CreateManual(ctx context.Context, params CreateManualCompetitorParams) (CompetitorRecord, error) {
	if s == nil || s.db == nil {
		return CompetitorRecord{}, errors.New("competitor store database is required")
	}

	params, err := normalizeCreateManualCompetitorParams(params)
	if err != nil {
		return CompetitorRecord{}, err
	}

	record, err := scanCompetitorRecord(s.db.QueryRowContext(
		ctx,
		createManualCompetitorSQL,
		params.ID,
		params.BusinessID,
		params.TenantID,
		params.Name,
		params.Website,
		params.Aliases,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompetitorRecord{}, ErrNotFound
		}
		return CompetitorRecord{}, fmt.Errorf("create manual competitor: %w", err)
	}
	return record, nil
}

func (s *CompetitorStore) SetStatus(ctx context.Context, params SetCompetitorStatusParams) (CompetitorRecord, error) {
	if s == nil || s.db == nil {
		return CompetitorRecord{}, errors.New("competitor store database is required")
	}
	if err := validateUUIDv7("tenant id", params.TenantID); err != nil {
		return CompetitorRecord{}, err
	}
	if err := validateUUIDv7("competitor id", params.CompetitorID); err != nil {
		return CompetitorRecord{}, err
	}
	if params.Status != CompetitorStatusTracked && params.Status != CompetitorStatusDismissed {
		return CompetitorRecord{}, errors.New("competitor status must be tracked or dismissed")
	}

	record, err := scanCompetitorRecord(s.db.QueryRowContext(
		ctx,
		setCompetitorStatusSQL,
		params.CompetitorID,
		params.TenantID,
		params.Status,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompetitorRecord{}, ErrNotFound
		}
		return CompetitorRecord{}, fmt.Errorf("set competitor status: %w", err)
	}
	return record, nil
}

func (s *CompetitorStore) ApproveSuggestedAlias(ctx context.Context, params SuggestedAliasParams) (CompetitorRecord, error) {
	return s.updateSuggestedAlias(ctx, params, approveSuggestedAliasSQL, "approve")
}

func (s *CompetitorStore) RejectSuggestedAlias(ctx context.Context, params SuggestedAliasParams) (CompetitorRecord, error) {
	return s.updateSuggestedAlias(ctx, params, rejectSuggestedAliasSQL, "reject")
}

func (s *CompetitorStore) UpdateAliases(ctx context.Context, params UpdateCompetitorAliasesParams) (CompetitorRecord, error) {
	if s == nil || s.db == nil {
		return CompetitorRecord{}, errors.New("competitor store database is required")
	}
	if err := validateUUIDv7("tenant id", params.TenantID); err != nil {
		return CompetitorRecord{}, err
	}
	if err := validateUUIDv7("competitor id", params.CompetitorID); err != nil {
		return CompetitorRecord{}, err
	}
	params.Aliases = normalizeAliases(params.Aliases)
	record, err := scanCompetitorRecord(s.db.QueryRowContext(
		ctx, updateCompetitorAliasesSQL, params.CompetitorID, params.TenantID, params.Aliases,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompetitorRecord{}, ErrNotFound
		}
		return CompetitorRecord{}, fmt.Errorf("update competitor aliases: %w", err)
	}
	return record, nil
}

func (s *CompetitorStore) updateSuggestedAlias(ctx context.Context, params SuggestedAliasParams, query, action string) (CompetitorRecord, error) {
	if s == nil || s.db == nil {
		return CompetitorRecord{}, errors.New("competitor store database is required")
	}
	params, err := normalizeSuggestedAliasParams(params)
	if err != nil {
		return CompetitorRecord{}, err
	}
	record, err := scanCompetitorRecord(s.db.QueryRowContext(
		ctx,
		query,
		params.CompetitorID,
		params.TenantID,
		params.Alias,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompetitorRecord{}, ErrNotFound
		}
		return CompetitorRecord{}, fmt.Errorf("%s suggested competitor alias: %w", action, err)
	}
	return record, nil
}

func scanCompetitorRecord(row rowScanner) (CompetitorRecord, error) {
	var record CompetitorRecord
	var aliases, suggestedAliases stringSlice
	if err := row.Scan(
		&record.ID,
		&record.BusinessID,
		&record.Name,
		&record.Website,
		&aliases,
		&suggestedAliases,
		&record.Source,
		&record.Status,
		&record.CreatedAt,
	); err != nil {
		return CompetitorRecord{}, err
	}
	record.Aliases = emptyIfNil(aliases)
	record.SuggestedAliases = emptyIfNil(suggestedAliases)
	return record, nil
}

func normalizeSuggestedAliasParams(params SuggestedAliasParams) (SuggestedAliasParams, error) {
	if err := validateUUIDv7("tenant id", params.TenantID); err != nil {
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
	if err := validateUUIDv7("tenant id", params.TenantID); err != nil {
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
