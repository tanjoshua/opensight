package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/domain"
)

// ErrBusinessNotDraft is returned when an apply-only operation targets a
// business that is not in draft status (already applied, or never drafted).
var ErrBusinessNotDraft = errors.New("business is not in draft status")

const (
	// lockDraftBusinessSQL locks the business row scoped by tenant so a concurrent
	// double-submit serializes here. The status is read under the lock to reject a
	// non-draft business before any write.
	lockDraftBusinessSQL = `SELECT status FROM businesses WHERE id = $1 AND tenant_id = $2 FOR UPDATE`

	// activateBusinessSQL writes the reviewed profile columns and flips the row to
	// active in one statement. Setting status, category, location, and activated_at
	// together satisfies businesses_active_profile_check in a single write. website
	// is intentionally not touched — it is set at business creation and is not part
	// of the proposal payload.
	activateBusinessSQL = `
UPDATE businesses
SET name = $2, aliases = $3, category = $4,
    services = $5::jsonb, location = $6::jsonb,
    status = 'active', activated_at = $7
WHERE id = $1
RETURNING ` + businessColumns

	// markProposalAppliedSQL resolves the pending proposal. Zero rows affected is
	// legitimate: manual-setup businesses never had a pending proposal.
	markProposalAppliedSQL = `
UPDATE profile_proposals
SET status = 'applied', resolved_at = now()
WHERE business_id = $1 AND status = 'pending'`
)

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

// ApplyProposalStore performs the cross-domain apply operation in one
// transaction: activate the business, insert its active prompts, and resolve the
// pending proposal. It owns no independent SQL beyond the business and proposal
// updates — prompt insertion delegates to createActivePromptInTx so the plan
// prompt-limit invariant is enforced identically.
type ApplyProposalStore struct {
	db *sql.DB
}

// NewApplyProposalStore returns an ApplyProposalStore backed by db.
func NewApplyProposalStore(db *sql.DB) *ApplyProposalStore {
	return &ApplyProposalStore{db: db}
}

// Apply activates a draft business with the reviewed profile and prompts. It is
// the only path that writes profile values to businesses (the 02 invariant). A
// missing or cross-tenant business returns ErrNotFound; a non-draft business
// returns ErrBusinessNotDraft; exceeding the plan prompt limit returns
// ErrPromptLimitExceeded. All-or-nothing: any failure rolls the whole tx back.
func (s *ApplyProposalStore) Apply(ctx context.Context, params ApplyProposalParams) (ApplyProposalResult, error) {
	if s == nil || s.db == nil {
		return ApplyProposalResult{}, errors.New("apply proposal store database is required")
	}

	var result ApplyProposalResult
	err := withTx(ctx, s.db, func(q querier) error {
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

func applyProposalInTx(ctx context.Context, q querier, params ApplyProposalParams) (ApplyProposalResult, error) {
	params, err := normalizeApplyProposalParams(params)
	if err != nil {
		return ApplyProposalResult{}, err
	}

	var status BusinessStatus
	if err := q.queryRowContext(ctx, lockDraftBusinessSQL, params.BusinessID, params.TenantID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ApplyProposalResult{}, ErrNotFound
		}
		return ApplyProposalResult{}, fmt.Errorf("lock draft business: %w", err)
	}
	if status != BusinessStatusDraft {
		return ApplyProposalResult{}, ErrBusinessNotDraft
	}

	business, err := scanBusiness(q.queryRowContext(
		ctx,
		activateBusinessSQL,
		params.BusinessID,
		params.Name,
		params.Aliases,
		params.Category,
		string(params.Services),
		jsonbArg(params.Location),
		params.ActivatedAt,
	))
	if err != nil {
		return ApplyProposalResult{}, fmt.Errorf("activate business: %w", err)
	}

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

	if _, err := q.execContext(ctx, markProposalAppliedSQL, params.BusinessID); err != nil {
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
