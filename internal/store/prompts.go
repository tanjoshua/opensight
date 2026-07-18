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

var (
	// ErrBusinessNotFound is returned when a prompt write targets an unknown
	// business.
	ErrBusinessNotFound = errors.New("business not found")

	// ErrPromptLimitExceeded is returned when adding an active prompt would
	// exceed the tenant's plan limit.
	ErrPromptLimitExceeded = errors.New("active prompt limit exceeded")
)

// PromptStatus is the persisted lifecycle status for a prompt.
type PromptStatus string

const (
	// PromptStatusActive means a prompt is included in future monitoring runs.
	PromptStatusActive PromptStatus = "active"
)

const (
	lockBusinessPromptLimitSQL = `
SELECT p.prompt_limit
FROM businesses b
JOIN tenants t ON t.id = b.tenant_id
JOIN plans p ON p.id = t.plan_id
WHERE b.id = $1
FOR UPDATE OF b`

	countActivePromptsSQL = `
SELECT count(*)
FROM prompts
WHERE business_id = $1
  AND status = 'active'`

	insertActivePromptSQL = `
INSERT INTO prompts (id, business_id, text, status, replaces_prompt_id)
VALUES ($1, $2, $3, 'active', $4)
RETURNING created_at`
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
// uuid.Nil, CreateActivePrompt generates a UUIDv7.
type CreateActivePromptParams struct {
	ID               domain.ID
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

// CreateActivePrompt inserts an active prompt only if doing so keeps
// count(active prompts) <= plan.prompt_limit for the prompt's business. It
// locks the business row before counting so concurrent prompt inserts for the
// same business serialize through this code path.
func (s *PromptStore) CreateActivePrompt(ctx context.Context, params CreateActivePromptParams) (Prompt, error) {
	if s == nil || s.db == nil {
		return Prompt{}, errors.New("prompt store database is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Prompt{}, fmt.Errorf("begin create prompt transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	prompt, err := createActivePromptInTx(ctx, sqlPromptTx{tx: tx}, params)
	if err != nil {
		return Prompt{}, err
	}
	if err := tx.Commit(); err != nil {
		return Prompt{}, fmt.Errorf("commit create prompt transaction: %w", err)
	}
	committed = true

	return prompt, nil
}

type promptQuerier interface {
	queryRowContext(ctx context.Context, query string, args ...any) rowScanner
}

type rowScanner interface {
	Scan(dest ...any) error
}

type sqlPromptTx struct {
	tx *sql.Tx
}

func (tx sqlPromptTx) queryRowContext(ctx context.Context, query string, args ...any) rowScanner {
	return tx.tx.QueryRowContext(ctx, query, args...)
}

func createActivePromptInTx(ctx context.Context, tx promptQuerier, params CreateActivePromptParams) (Prompt, error) {
	params, err := normalizeCreateActivePromptParams(params)
	if err != nil {
		return Prompt{}, err
	}

	var promptLimit int
	if err := tx.queryRowContext(ctx, lockBusinessPromptLimitSQL, params.BusinessID).Scan(&promptLimit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Prompt{}, ErrBusinessNotFound
		}
		return Prompt{}, fmt.Errorf("load business prompt limit: %w", err)
	}

	var activePromptCount int
	if err := tx.queryRowContext(ctx, countActivePromptsSQL, params.BusinessID).Scan(&activePromptCount); err != nil {
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
	if err := tx.queryRowContext(
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

func validateUUIDv7(name string, id domain.ID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%s is required", name)
	}
	if id.Version() != uuid.Version(7) {
		return fmt.Errorf("%s must be UUIDv7", name)
	}
	return nil
}
