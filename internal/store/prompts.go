package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"

	"github.com/google/uuid"
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

const (
	// lockBusinessPlanCodeSQL locks the business row and loads its tenant's
	// plan_code only when the business belongs to the given tenant. No rows
	// means the business is missing or owned by another tenant — either way
	// ErrNotFound, with no cross-tenant existence oracle. The prompt limit
	// itself is resolved from the catalog (billing.PlanFor) after this lock, not
	// read from the database.
	lockBusinessPlanCodeSQL = `
SELECT s.plan_code
FROM businesses b
JOIN subscriptions s ON s.tenant_id = b.tenant_id
WHERE b.id = $1 AND b.tenant_id = $2
FOR UPDATE OF b`

	// countActivePromptsSQL is tenant-safe because it always runs after
	// lockBusinessPromptLimitSQL has proven, in the same transaction, that the
	// business belongs to the tenant.
	countActivePromptsSQL = `
SELECT count(*)
FROM prompts
WHERE business_id = $1
  AND status = 'active'`

	insertActivePromptSQL = `
INSERT INTO prompts (id, business_id, text, status, replaces_prompt_id)
VALUES ($1, $2, $3, 'active', $4)
RETURNING created_at`

	listActivePromptsSQL = `
SELECT id, business_id, text, status, replaces_prompt_id, created_at
FROM prompts
WHERE business_id = $1
  AND status = 'active'
ORDER BY created_at`

	getPromptSQL = `
SELECT pr.id, pr.business_id, pr.text, pr.status, pr.replaces_prompt_id, pr.created_at
FROM prompts pr
JOIN businesses b ON b.id = pr.business_id
WHERE pr.id = $1 AND b.tenant_id = $2`

	// lockPromptForReplaceSQL loads and row-locks the prompt to be replaced,
	// scoped to the tenant via the business join in one statement. FOR UPDATE OF
	// pr serializes concurrent replaces on the same prompt: the second waits, then
	// sees status = 'retired' and is rejected. A missing or cross-tenant prompt
	// returns no rows → ErrNotFound.
	lockPromptForReplaceSQL = `
SELECT pr.id, pr.business_id, pr.text, pr.status, pr.replaces_prompt_id, pr.created_at
FROM prompts pr
JOIN businesses b ON b.id = pr.business_id
WHERE pr.id = $1 AND b.tenant_id = $2
FOR UPDATE OF pr`

	retirePromptSQL = `
UPDATE prompts SET status = 'retired', retired_at = now() WHERE id = $1`
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

// PromptStore writes prompt rows while enforcing prompt-specific invariants.
type PromptStore struct {
	db *sql.DB
}

// NewPromptStore returns a PromptStore backed by db.
func NewPromptStore(db *sql.DB) *PromptStore {
	return &PromptStore{db: db}
}

// CreateActivePrompt inserts an active prompt only if the business belongs to
// params.TenantID and doing so keeps count(active prompts) <= billing.Plan.PromptLimit
// for the business. It locks the business row (scoped by tenant) before counting
// so concurrent prompt inserts for the same business serialize through this code
// path. A missing or cross-tenant business returns ErrNotFound.
func (s *PromptStore) CreateActivePrompt(ctx context.Context, params CreateActivePromptParams) (Prompt, error) {
	if s == nil || s.db == nil {
		return Prompt{}, errors.New("prompt store database is required")
	}

	var prompt Prompt
	err := withTx(ctx, s.db, func(q querier) error {
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
func (s *PromptStore) ReplacePrompt(ctx context.Context, params ReplacePromptParams) (Prompt, error) {
	if s == nil || s.db == nil {
		return Prompt{}, errors.New("prompt store database is required")
	}

	var prompt Prompt
	err := withTx(ctx, s.db, func(q querier) error {
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

func replacePromptInTx(ctx context.Context, q querier, params ReplacePromptParams) (Prompt, error) {
	old, err := scanPrompt(q.queryRowContext(ctx, lockPromptForReplaceSQL, params.OldPromptID, params.TenantID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Prompt{}, ErrNotFound
		}
		return Prompt{}, fmt.Errorf("lock prompt for replace: %w", err)
	}
	if old.Status != PromptStatusActive {
		return Prompt{}, ErrPromptNotActive
	}

	if _, err := q.execContext(ctx, retirePromptSQL, old.ID); err != nil {
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
func (s *PromptStore) ListActivePrompts(ctx context.Context, tenantID, businessID domain.ID) ([]Prompt, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("prompt store database is required")
	}

	q := sqlQuerier{q: s.db}
	if err := businessOwned(ctx, q, tenantID, businessID); err != nil {
		return nil, err
	}

	rows, err := q.queryContext(ctx, listActivePromptsSQL, businessID)
	if err != nil {
		return nil, fmt.Errorf("list active prompts: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	prompts := []Prompt{}
	for rows.Next() {
		prompt, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		prompts = append(prompts, prompt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active prompts: %w", err)
	}
	return prompts, nil
}

// GetPrompt loads a single prompt by id, scoped to the tenant via the business
// join in one statement (deep-by-id). A missing or cross-tenant prompt returns
// ErrNotFound.
func (s *PromptStore) GetPrompt(ctx context.Context, tenantID, promptID domain.ID) (Prompt, error) {
	if s == nil || s.db == nil {
		return Prompt{}, errors.New("prompt store database is required")
	}

	prompt, err := scanPrompt(s.db.QueryRowContext(ctx, getPromptSQL, promptID, tenantID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Prompt{}, ErrNotFound
		}
		return Prompt{}, fmt.Errorf("get prompt: %w", err)
	}
	return prompt, nil
}

func scanPrompt(row rowScanner) (Prompt, error) {
	var prompt Prompt
	if err := row.Scan(
		&prompt.ID,
		&prompt.BusinessID,
		&prompt.Text,
		&prompt.Status,
		&prompt.ReplacesPromptID,
		&prompt.CreatedAt,
	); err != nil {
		return Prompt{}, err
	}
	return prompt, nil
}

func createActivePromptInTx(ctx context.Context, q querier, params CreateActivePromptParams) (Prompt, error) {
	params, err := normalizeCreateActivePromptParams(params)
	if err != nil {
		return Prompt{}, err
	}

	var planCode string
	if err := q.queryRowContext(ctx, lockBusinessPlanCodeSQL, params.BusinessID, params.TenantID).Scan(&planCode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Prompt{}, ErrNotFound
		}
		return Prompt{}, fmt.Errorf("load business plan code: %w", err)
	}
	plan, err := billing.PlanFor(planCode)
	if err != nil {
		return Prompt{}, fmt.Errorf("resolve plan: %w", err)
	}
	promptLimit := plan.PromptLimit

	var activePromptCount int
	if err := q.queryRowContext(ctx, countActivePromptsSQL, params.BusinessID).Scan(&activePromptCount); err != nil {
		return Prompt{}, fmt.Errorf("count active prompts: %w", err)
	}
	if activePromptCount >= promptLimit {
		return Prompt{}, fmt.Errorf(
			"%w: active prompts %d >= plan limit %d",
			ErrPromptLimitExceeded,
			activePromptCount,
			promptLimit,
		)
	}

	var replacesPromptID any
	if params.ReplacesPromptID != nil {
		replacesPromptID = *params.ReplacesPromptID
	}

	prompt := Prompt{
		ID:               params.ID,
		BusinessID:       params.BusinessID,
		Text:             params.Text,
		Status:           PromptStatusActive,
		ReplacesPromptID: params.ReplacesPromptID,
	}
	if err := q.queryRowContext(
		ctx,
		insertActivePromptSQL,
		params.ID,
		params.BusinessID,
		params.Text,
		replacesPromptID,
	).Scan(&prompt.CreatedAt); err != nil {
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
