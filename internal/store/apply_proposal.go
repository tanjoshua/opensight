package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// ErrBusinessNotDraft is returned when an apply-only operation targets a
// business that is not in draft status (already applied, or never drafted).
var ErrBusinessNotDraft = errors.New("business is not in draft status")

// ApplyProposalParams are the reviewed, user-edited values written when a draft
// business is activated (design 03, "Review and apply"). Nil Services default to
// an empty JSON array, mirroring CreateBusinessParams.
type ApplyProposalParams struct {
	TenantID    domain.ID
	BusinessID  domain.ID
	Name        string
	Aliases     []string
	Category    string
	Services    json.RawMessage // jsonb array
	Location    json.RawMessage // jsonb object
	PromptTexts []string
	ActivatedAt time.Time
}

// ApplyProposalResult is the activated business and its inserted active prompts.
type ApplyProposalResult struct {
	Business Business
	Prompts  []Prompt
}

// Apply activates a draft business with the reviewed profile and prompts, in
// one transaction. Prompt insertion delegates to createActivePromptInTx so the
// plan prompt-limit invariant is enforced identically. It is
// the only path that writes profile values to businesses (the 02 invariant). A
// missing or cross-tenant business returns ErrNotFound; a non-draft business
// returns ErrBusinessNotDraft; exceeding the plan prompt limit returns
// ErrPromptLimitExceeded. All-or-nothing: any failure rolls the whole tx back.
func (s *Store) Apply(ctx context.Context, params ApplyProposalParams) (ApplyProposalResult, error) {

	var result ApplyProposalResult
	err := s.withTx(ctx, func(q *storesqlc.Queries) error {
		applied, err := applyProposalInTx(ctx, q, params)
		if err != nil {
			return err
		}
		result = applied
		return nil
	})
	if err != nil {
		return ApplyProposalResult{}, err
	}
	return result, nil
}

func applyProposalInTx(ctx context.Context, q *storesqlc.Queries, params ApplyProposalParams) (ApplyProposalResult, error) {
	params, err := normalizeApplyProposalParams(params)
	if err != nil {
		return ApplyProposalResult{}, err
	}

	status, err := q.LockDraftBusiness(ctx, storesqlc.LockDraftBusinessParams{ID: params.BusinessID, TenantID: params.TenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ApplyProposalResult{}, ErrNotFound
		}
		return ApplyProposalResult{}, fmt.Errorf("lock draft business: %w", err)
	}
	if BusinessStatus(status) != BusinessStatusDraft {
		return ApplyProposalResult{}, ErrBusinessNotDraft
	}

	location := params.Location
	category := params.Category
	activatedAt := params.ActivatedAt
	row, err := q.ActivateBusiness(ctx, storesqlc.ActivateBusinessParams{
		ID: params.BusinessID, Name: params.Name, Aliases: params.Aliases, Category: &category,
		Services: params.Services, Location: &location, ActivatedAt: &activatedAt,
	})
	if err != nil {
		return ApplyProposalResult{}, fmt.Errorf("activate business: %w", err)
	}
	business := businessFromSQLC(row)

	prompts := make([]Prompt, 0, len(params.PromptTexts))
	for _, text := range params.PromptTexts {
		prompt, err := createActivePromptInTx(ctx, q, CreateActivePromptParams{
			TenantID:   params.TenantID,
			BusinessID: params.BusinessID,
			Text:       text,
		})
		if err != nil {
			return ApplyProposalResult{}, err
		}
		prompts = append(prompts, prompt)
	}

	if err := q.MarkProposalApplied(ctx, params.BusinessID); err != nil {
		return ApplyProposalResult{}, fmt.Errorf("mark proposal applied: %w", err)
	}

	return ApplyProposalResult{Business: business, Prompts: prompts}, nil
}

func normalizeApplyProposalParams(params ApplyProposalParams) (ApplyProposalParams, error) {
	if err := validateUUIDv7("tenant id", params.TenantID); err != nil {
		return ApplyProposalParams{}, err
	}
	if err := validateUUIDv7("business id", params.BusinessID); err != nil {
		return ApplyProposalParams{}, err
	}
	if strings.TrimSpace(params.Name) == "" {
		return ApplyProposalParams{}, errors.New("business name is required")
	}
	if strings.TrimSpace(params.Category) == "" {
		return ApplyProposalParams{}, errors.New("business category is required")
	}
	if params.ActivatedAt.IsZero() {
		return ApplyProposalParams{}, errors.New("activated at is required")
	}
	if params.Aliases == nil {
		params.Aliases = []string{}
	}
	if len(params.Services) == 0 {
		params.Services = json.RawMessage("[]")
	}
	return params, nil
}
