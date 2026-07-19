package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opensight/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicateResult is returned when a prompt result already exists for a
// (run, prompt) pair. The store never silently absorbs the conflict: RUN-4's
// retry path re-gets the existing row on this error.
var ErrDuplicateResult = errors.New("prompt result already exists for run and prompt")

// ResultStatus is the persisted outcome of a single prompt call.
type ResultStatus string

const (
	// ResultStatusSucceeded is a completed call with a response.
	ResultStatusSucceeded ResultStatus = "succeeded"
	// ResultStatusFailed is a call that errored.
	ResultStatusFailed ResultStatus = "failed"
)

// PromptResult is a persisted prompt_results row (migration 00004). Results are
// append-only; there is no update method.
type PromptResult struct {
	ID           domain.ID
	RunID        domain.ID
	PromptID     domain.ID
	Status       ResultStatus
	Model        *string
	Request      json.RawMessage
	RawResponse  json.RawMessage
	ResponseText *string
	Error        *string
	RequestedAt  time.Time
	CompletedAt  time.Time
}

// CreateResultParams are the inputs for CreateResult. If ID is uuid.Nil a
// UUIDv7 is generated. Zero RequestedAt/CompletedAt fall back to now() in the
// database. The status/payload invariants are enforced by table CHECKs.
type CreateResultParams struct {
	ID           domain.ID
	RunID        domain.ID
	PromptID     domain.ID
	Status       ResultStatus
	Model        *string
	Request      json.RawMessage
	RawResponse  json.RawMessage
	ResponseText *string
	Error        *string
	RequestedAt  time.Time
	CompletedAt  time.Time
}

// ResultFilter narrows ListResults. All predicates are optional; Limit/Offset
// apply as given when > 0 (the handler chooses defaults and caps).
type ResultFilter struct {
	RunID    *domain.ID
	PromptID *domain.ID
	Status   *ResultStatus
	Limit    int
	Offset   int
}

// ResultStore reads and writes prompt_results rows.
type ResultStore struct {
	db *sql.DB
}

// NewResultStore returns a ResultStore backed by db.
func NewResultStore(db *sql.DB) *ResultStore {
	return &ResultStore{db: db}
}

const (
	resultColumns = `pr.id, pr.run_id, pr.prompt_id, pr.status, pr.model, pr.request,
       pr.raw_response, pr.response_text, pr.error, pr.requested_at, pr.completed_at`

	// runOwnedSQL verifies, inside CreateResult's transaction, that the target
	// run belongs to the tenant before the append-only insert.
	runOwnedSQL = `
SELECT 1
FROM monitoring_runs r
JOIN businesses b ON b.id = r.business_id
WHERE r.id = $1 AND b.tenant_id = $2`

	insertResultSQL = `
INSERT INTO prompt_results (
  id, run_id, prompt_id, status, model, request, raw_response, response_text, error,
  requested_at, completed_at
) VALUES (
  $1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9,
  COALESCE($10, now()), COALESCE($11, now())
)
RETURNING requested_at, completed_at`

	getResultByRunAndPromptSQL = `
SELECT ` + resultColumns + `
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
WHERE pr.run_id = $1 AND pr.prompt_id = $2 AND b.tenant_id = $3`

	getResultSQL = `
SELECT ` + resultColumns + `
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
WHERE pr.id = $1 AND b.tenant_id = $2`
)

// CreateResult appends a prompt result, scoped by a tenant-predicated lookup of
// the run's business inside one transaction. A missing or cross-tenant run
// returns ErrNotFound; a UNIQUE (run_id, prompt_id) violation returns
// ErrDuplicateResult. RUN-4's activity retry re-gets the existing row on the
// latter (get -> miss -> create -> on ErrDuplicateResult re-get).
func (s *ResultStore) CreateResult(ctx context.Context, tenantID domain.ID, params CreateResultParams) (PromptResult, error) {
	if s == nil || s.db == nil {
		return PromptResult{}, errors.New("result store database is required")
	}

	params, err := normalizeCreateResultParams(params)
	if err != nil {
		return PromptResult{}, err
	}
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return PromptResult{}, err
	}

	result := PromptResult{
		ID:           params.ID,
		RunID:        params.RunID,
		PromptID:     params.PromptID,
		Status:       params.Status,
		Model:        params.Model,
		Request:      params.Request,
		RawResponse:  params.RawResponse,
		ResponseText: params.ResponseText,
		Error:        params.Error,
	}
	err = withTx(ctx, s.db, func(q querier) error {
		var one int
		if err := q.queryRowContext(ctx, runOwnedSQL, params.RunID, tenantID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("verify run ownership: %w", err)
		}
		if err := q.queryRowContext(
			ctx,
			insertResultSQL,
			params.ID,
			params.RunID,
			params.PromptID,
			string(params.Status),
			params.Model,
			string(params.Request),
			jsonbArg(params.RawResponse),
			params.ResponseText,
			params.Error,
			nullableTime(params.RequestedAt),
			nullableTime(params.CompletedAt),
		).Scan(&result.RequestedAt, &result.CompletedAt); err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicateResult
			}
			return fmt.Errorf("insert prompt result: %w", err)
		}
		return nil
	})
	if err != nil {
		return PromptResult{}, err
	}
	return result, nil
}

// GetResultByRunAndPrompt loads the result for a (run, prompt) pair, tenant
// scoped via the business join (deep-by-id). No row returns ErrNotFound. RUN-4
// calls this first for its idempotency check and after an ErrDuplicateResult
// race.
func (s *ResultStore) GetResultByRunAndPrompt(ctx context.Context, tenantID, runID, promptID domain.ID) (PromptResult, error) {
	if s == nil || s.db == nil {
		return PromptResult{}, errors.New("result store database is required")
	}

	result, err := scanResult(s.db.QueryRowContext(ctx, getResultByRunAndPromptSQL, runID, promptID, tenantID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PromptResult{}, ErrNotFound
		}
		return PromptResult{}, fmt.Errorf("get result by run and prompt: %w", err)
	}
	return result, nil
}

// GetResult loads a single result by id, tenant scoped via
// prompt_results -> monitoring_runs -> businesses (WEB-2 GET /results/:id). A
// missing or cross-tenant result returns ErrNotFound.
func (s *ResultStore) GetResult(ctx context.Context, tenantID, resultID domain.ID) (PromptResult, error) {
	if s == nil || s.db == nil {
		return PromptResult{}, errors.New("result store database is required")
	}

	result, err := scanResult(s.db.QueryRowContext(ctx, getResultSQL, resultID, tenantID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PromptResult{}, ErrNotFound
		}
		return PromptResult{}, fmt.Errorf("get result: %w", err)
	}
	return result, nil
}

// ListResults returns a business's results with optional hard-coded predicates
// (WEB-2). It enters through the tenant-checked business lookup, then joins
// results up to the business so foreign run/prompt filters yield nothing rather
// than leaking across tenants.
func (s *ResultStore) ListResults(ctx context.Context, tenantID, businessID domain.ID, filter ResultFilter) ([]PromptResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("result store database is required")
	}

	q := sqlQuerier{q: s.db}
	if err := businessOwned(ctx, q, tenantID, businessID); err != nil {
		return nil, err
	}

	query := `
SELECT ` + resultColumns + `
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
WHERE r.business_id = $1
  AND ($2::uuid IS NULL OR pr.run_id = $2)
  AND ($3::uuid IS NULL OR pr.prompt_id = $3)
  AND ($4::text IS NULL OR pr.status = $4)
ORDER BY pr.requested_at DESC`

	args := []any{businessID, filter.RunID, filter.PromptID, statusArg(filter.Status)}
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += " LIMIT $" + strconv.Itoa(len(args))
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		query += " OFFSET $" + strconv.Itoa(len(args))
	}

	rows, err := q.queryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list results: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	results := []PromptResult{}
	for rows.Next() {
		result, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate results: %w", err)
	}
	return results, nil
}

func scanResult(row rowScanner) (PromptResult, error) {
	var result PromptResult
	if err := row.Scan(
		&result.ID,
		&result.RunID,
		&result.PromptID,
		&result.Status,
		&result.Model,
		&result.Request,
		&result.RawResponse,
		&result.ResponseText,
		&result.Error,
		&result.RequestedAt,
		&result.CompletedAt,
	); err != nil {
		return PromptResult{}, err
	}
	return result, nil
}

func normalizeCreateResultParams(params CreateResultParams) (CreateResultParams, error) {
	if params.ID == uuid.Nil {
		id, err := domain.NewID()
		if err != nil {
			return CreateResultParams{}, err
		}
		params.ID = id
	}
	if err := validateUUIDv7("result id", params.ID); err != nil {
		return CreateResultParams{}, err
	}
	if err := validateUUIDv7("run id", params.RunID); err != nil {
		return CreateResultParams{}, err
	}
	if err := validateUUIDv7("prompt id", params.PromptID); err != nil {
		return CreateResultParams{}, err
	}
	if params.Status != ResultStatusSucceeded && params.Status != ResultStatusFailed {
		return CreateResultParams{}, fmt.Errorf("result status must be %q or %q", ResultStatusSucceeded, ResultStatusFailed)
	}
	if len(params.Request) == 0 {
		return CreateResultParams{}, errors.New("result request payload is required")
	}
	return params, nil
}

func statusArg(status *ResultStatus) any {
	if status == nil {
		return nil
	}
	return string(*status)
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
