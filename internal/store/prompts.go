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

// ErrPromptLimitExceeded is returned when adding an active prompt would exceed
// the tenant's plan limit.
var ErrPromptLimitExceeded = errors.New("active prompt limit exceeded")

// PromptStatus is the persisted lifecycle status for a prompt.
type PromptStatus string

const (
	// PromptStatusActive means a prompt is included in future monitoring runs.
	PromptStatusActive PromptStatus = "active"
	// PromptStatusRetired means a prompt is excluded from future runs.
	PromptStatusRetired PromptStatus = "retired"
)

const (
	// lockBusinessPromptLimitSQL locks the business row and loads its plan
	// prompt limit only when the business belongs to the given tenant. No rows
	// means the business is missing or owned by another tenant — either way
	// ErrNotFound, with no cross-tenant existence oracle.
	lockBusinessPromptLimitSQL = `
SELECT p.prompt_limit
FROM businesses b
JOIN tenants t ON t.id = b.tenant_id
JOIN plans p ON p.id = t.plan_id
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

// PromptStore writes prompt rows while enforcing prompt-specific invariants.
type PromptStore struct {
	db *sql.DB
}

// NewPromptStore returns a PromptStore backed by db.
func NewPromptStore(db *sql.DB) *PromptStore {
	return &PromptStore{db: db}
}

// CreateActivePrompt inserts an active prompt only if the business belongs to
// params.TenantID and doing so keeps count(active prompts) <= plan.prompt_limit
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

	var promptLimit int
	if err := q.queryRowContext(ctx, lockBusinessPromptLimitSQL, params.BusinessID, params.TenantID).Scan(&promptLimit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Prompt{}, ErrNotFound
		}
		return Prompt{}, fmt.Errorf("load business prompt limit: %w", err)
	}

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
