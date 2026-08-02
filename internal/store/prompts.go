package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrPromptLimitExceeded is returned when adding an active prompt would exceed
// the tenant's plan limit.
var ErrPromptLimitExceeded = errors.New("active prompt limit exceeded")

// ErrPromptNotActive is returned when a replace targets a prompt that is not
// active (already retired, or concurrently retired by a racing replace).
var ErrPromptNotActive = errors.New("prompt is not active")

// PromptStatus is the persisted lifecycle status for a prompt.
type PromptStatus string

const (
	// PromptStatusActive means a prompt is included in future monitoring runs.
	PromptStatusActive PromptStatus = "active"
	// PromptStatusRetired means a prompt is excluded from future runs.
	PromptStatusRetired PromptStatus = "retired"
)

// Prompt is a persisted prompt row.
type Prompt struct {
	ID               domain.ID
	BusinessID       domain.ID
	Text             string
	Status           PromptStatus
	ReplacesPromptID *domain.ID
	CreatedAt        time.Time
}

// CreateActivePromptParams are the inputs for adding an active prompt. If ID is
// uuid.Nil, CreateActivePrompt generates a UUIDv7. TenantID is required and
// validated against the business's owner.
type CreateActivePromptParams struct {
	ID               domain.ID
	TenantID         domain.ID
	BusinessID       domain.ID
	Text             string
	ReplacesPromptID *domain.ID
}

// ReplacePromptParams are the inputs for replacing an active prompt: retire the
// old prompt and insert a new active one that records replaces_prompt_id. Text
// is immutable, so an edit is expressed as a replace with the new text.
type ReplacePromptParams struct {
	TenantID    domain.ID
	OldPromptID domain.ID
	Text        string
}

// CreateActivePrompt inserts an active prompt only if the business belongs to
// params.TenantID and doing so keeps count(active prompts) <= billing.Plan.PromptLimit
// for the business. It locks the business row (scoped by tenant) before counting
// so concurrent prompt inserts for the same business serialize through this code
// path. A missing or cross-tenant business returns ErrNotFound.
func (s *Store) CreateActivePrompt(ctx context.Context, params CreateActivePromptParams) (Prompt, error) {

	var prompt Prompt
	err := s.withTx(ctx, func(q *storesqlc.Queries) error {
		created, err := createActivePromptInTx(ctx, q, params)
		if err != nil {
			return err
		}
		prompt = created
		return nil
	})
	if err != nil {
		return Prompt{}, err
	}
	return prompt, nil
}

// ReplacePrompt retires an active prompt and inserts a new active prompt that
// records it as its predecessor, atomically. It row-locks the old prompt first
// (tenant-scoped): a missing or cross-tenant prompt is ErrNotFound, a non-active
// prompt is ErrPromptNotActive. The insert reuses createActivePromptInTx, so the
// plan prompt limit is enforced against the post-retire count in the same
// transaction (the retired old prompt no longer counts). Lock order is
// prompt-row then business-row, consistent with CreateActivePrompt only ever
// locking the business row.
func (s *Store) ReplacePrompt(ctx context.Context, params ReplacePromptParams) (Prompt, error) {

	var prompt Prompt
	err := s.withTx(ctx, func(q *storesqlc.Queries) error {
		created, err := replacePromptInTx(ctx, q, params)
		if err != nil {
			return err
		}
		prompt = created
		return nil
	})
	if err != nil {
		return Prompt{}, err
	}
	return prompt, nil
}

func replacePromptInTx(ctx context.Context, q *storesqlc.Queries, params ReplacePromptParams) (Prompt, error) {
	row, err := q.LockPromptForReplace(ctx, storesqlc.LockPromptForReplaceParams{ID: params.OldPromptID, TenantID: params.TenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Prompt{}, ErrNotFound
		}
		return Prompt{}, fmt.Errorf("lock prompt for replace: %w", err)
	}
	old := promptFromRow(row.ID, row.BusinessID, row.Text, row.Status, row.ReplacesPromptID, row.CreatedAt)
	if old.Status != PromptStatusActive {
		return Prompt{}, ErrPromptNotActive
	}

	if err := q.RetirePrompt(ctx, old.ID); err != nil {
		return Prompt{}, fmt.Errorf("retire prompt: %w", err)
	}

	return createActivePromptInTx(ctx, q, CreateActivePromptParams{
		TenantID:         params.TenantID,
		BusinessID:       old.BusinessID,
		Text:             params.Text,
		ReplacesPromptID: &old.ID,
	})
}

// ListActivePrompts returns the business's active prompts, oldest first. It
// enters through the tenant-checked business lookup so an empty result for a
// business the tenant does not own is reported as ErrNotFound rather than an
// empty slice.
func (s *Store) ListActivePrompts(ctx context.Context, tenantID, businessID domain.ID) ([]Prompt, error) {

	q := s.q(ctx)
	if err := businessOwned(ctx, q, tenantID, businessID); err != nil {
		return nil, err
	}

	rows, err := q.ListActivePrompts(ctx, businessID)
	if err != nil {
		return nil, fmt.Errorf("list active prompts: %w", err)
	}
	prompts := make([]Prompt, 0, len(rows))
	for _, row := range rows {
		prompts = append(prompts, promptFromRow(row.ID, row.BusinessID, row.Text, row.Status, row.ReplacesPromptID, row.CreatedAt))
	}
	return prompts, nil
}

// GetPrompt loads a single prompt by id, scoped to the tenant via the business
// join in one statement (deep-by-id). A missing or cross-tenant prompt returns
// ErrNotFound.
func (s *Store) GetPrompt(ctx context.Context, tenantID, promptID domain.ID) (Prompt, error) {

	row, err := s.q(ctx).GetPrompt(ctx, storesqlc.GetPromptParams{ID: promptID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Prompt{}, ErrNotFound
		}
		return Prompt{}, fmt.Errorf("get prompt: %w", err)
	}
	return promptFromRow(row.ID, row.BusinessID, row.Text, row.Status, row.ReplacesPromptID, row.CreatedAt), nil
}

func promptFromRow(id, businessID domain.ID, text, status string, replaces *domain.ID, created time.Time) Prompt {
	return Prompt{ID: id, BusinessID: businessID, Text: text, Status: PromptStatus(status), ReplacesPromptID: replaces, CreatedAt: created}
}

func createActivePromptInTx(ctx context.Context, q *storesqlc.Queries, params CreateActivePromptParams) (Prompt, error) {
	params, err := normalizeCreateActivePromptParams(params)
	if err != nil {
		return Prompt{}, err
	}

	planCode, err := q.LockBusinessPlanCode(ctx, storesqlc.LockBusinessPlanCodeParams{ID: params.BusinessID, TenantID: params.TenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Prompt{}, ErrNotFound
		}
		return Prompt{}, fmt.Errorf("load business plan code: %w", err)
	}
	plan, err := billing.PlanFor(planCode)
	if err != nil {
		return Prompt{}, fmt.Errorf("resolve plan: %w", err)
	}
	promptLimit := plan.PromptLimit

	activePromptCount, err := q.CountActivePrompts(ctx, params.BusinessID)
	if err != nil {
		return Prompt{}, fmt.Errorf("count active prompts: %w", err)
	}
	if activePromptCount >= int64(promptLimit) {
		return Prompt{}, fmt.Errorf(
			"%w: active prompts %d >= plan limit %d",
			ErrPromptLimitExceeded,
			activePromptCount,
			promptLimit,
		)
	}

	prompt := Prompt{
		ID:               params.ID,
		BusinessID:       params.BusinessID,
		Text:             params.Text,
		Status:           PromptStatusActive,
		ReplacesPromptID: params.ReplacesPromptID,
	}
	prompt.CreatedAt, err = q.InsertActivePrompt(ctx, storesqlc.InsertActivePromptParams{
		ID: params.ID, BusinessID: params.BusinessID, Text: params.Text, ReplacesPromptID: params.ReplacesPromptID,
	})
	if err != nil {
		return Prompt{}, fmt.Errorf("insert active prompt: %w", err)
	}

	return prompt, nil
}

func normalizeCreateActivePromptParams(params CreateActivePromptParams) (CreateActivePromptParams, error) {
	if params.ID == uuid.Nil {
		id, err := domain.NewID()
		if err != nil {
			return CreateActivePromptParams{}, err
		}
		params.ID = id
	}
	if err := validateUUIDv7("prompt id", params.ID); err != nil {
		return CreateActivePromptParams{}, err
	}
	if err := validateUUIDv7("tenant id", params.TenantID); err != nil {
		return CreateActivePromptParams{}, err
	}
	if err := validateUUIDv7("business id", params.BusinessID); err != nil {
		return CreateActivePromptParams{}, err
	}
	if params.ReplacesPromptID != nil {
		if err := validateUUIDv7("replaces prompt id", *params.ReplacesPromptID); err != nil {
			return CreateActivePromptParams{}, err
		}
		if *params.ReplacesPromptID == params.ID {
			return CreateActivePromptParams{}, errors.New("prompt cannot replace itself")
		}
	}
	if strings.TrimSpace(params.Text) == "" {
		return CreateActivePromptParams{}, errors.New("prompt text is required")
	}

	return params, nil
}
