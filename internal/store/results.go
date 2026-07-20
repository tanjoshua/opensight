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

// ResultDetail is the response-drawer read model for a prompt result joined to
// its prompt text and run metadata.
type ResultDetail struct {
	Result     PromptResult
	Prompt     Prompt
	Run        Run
	BusinessID domain.ID
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

// ResultListItem is one Responses-list row: the result joined to its prompt
// text (design 06 Phase 1 — the list is unreadable without the question asked).
type ResultListItem struct {
	PromptResult
	PromptText string
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

	// runPromptOwnedSQL verifies both that the run belongs to tenantID and that
	// the prompt being attached belongs to the same business as the run. The
	// schema has separate FKs for run_id and prompt_id; this check is the
	// app-layer composite integrity guard for WEB-2/RUN-4.
	runPromptOwnedSQL = `
SELECT 1
FROM monitoring_runs r
JOIN businesses b ON b.id = r.business_id
JOIN prompts p ON p.id = $2 AND p.business_id = r.business_id
WHERE r.id = $1 AND b.tenant_id = $3`

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

	getResultDetailSQL = `
SELECT ` + resultColumns + `,
       p.text,
       r.business_id, r.platform, r.trigger, r.scheduled_for, r.status,
       r.workflow_id, r.started_at, r.completed_at, r.analysis_completed_at
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
JOIN prompts p ON p.id = pr.prompt_id AND p.business_id = r.business_id
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
		if err := q.queryRowContext(ctx, runPromptOwnedSQL, params.RunID, params.PromptID, tenantID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("verify run/prompt ownership: %w", err)
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

// GetResultDetail loads one result with its prompt text and run metadata,
// tenant scoped through prompt_results -> monitoring_runs -> businesses.
func (s *ResultStore) GetResultDetail(ctx context.Context, tenantID, resultID domain.ID) (ResultDetail, error) {
	if s == nil || s.db == nil {
		return ResultDetail{}, errors.New("result store database is required")
	}

	var detail ResultDetail
	var promptText string
	err := s.db.QueryRowContext(ctx, getResultDetailSQL, resultID, tenantID).Scan(
		&detail.Result.ID,
		&detail.Result.RunID,
		&detail.Result.PromptID,
		&detail.Result.Status,
		&detail.Result.Model,
		&detail.Result.Request,
		nullableJSON{&detail.Result.RawResponse},
		&detail.Result.ResponseText,
		&detail.Result.Error,
		&detail.Result.RequestedAt,
		&detail.Result.CompletedAt,
		&promptText,
		&detail.Run.BusinessID,
		&detail.Run.Platform,
		&detail.Run.Trigger,
		&detail.Run.ScheduledFor,
		&detail.Run.Status,
		&detail.Run.WorkflowID,
		&detail.Run.StartedAt,
		&detail.Run.CompletedAt,
		&detail.Run.AnalysisCompletedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResultDetail{}, ErrNotFound
		}
		return ResultDetail{}, fmt.Errorf("get result detail: %w", err)
	}

	detail.BusinessID = detail.Run.BusinessID
	detail.Run.ID = detail.Result.RunID
	detail.Prompt = Prompt{
		ID:         detail.Result.PromptID,
		BusinessID: detail.Run.BusinessID,
		Text:       promptText,
	}
	return detail, nil
}

// ListResults returns a business's results with optional hard-coded predicates
// (WEB-2). It enters through the tenant-checked business lookup, then joins
// results up to the business so foreign run/prompt filters yield nothing rather
// than leaking across tenants.
func (s *ResultStore) ListResults(ctx context.Context, tenantID, businessID domain.ID, filter ResultFilter) ([]ResultListItem, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("result store database is required")
	}

	q := sqlQuerier{q: s.db}
	if err := businessOwned(ctx, q, tenantID, businessID); err != nil {
		return nil, err
	}

	query := `
SELECT ` + resultColumns + `, p.text
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN prompts p ON p.id = pr.prompt_id AND p.business_id = r.business_id
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

	results := []ResultListItem{}
	for rows.Next() {
		var item ResultListItem
		if err := rows.Scan(
			&item.ID,
			&item.RunID,
			&item.PromptID,
			&item.Status,
			&item.Model,
			&item.Request,
			nullableJSON{&item.RawResponse},
			&item.ResponseText,
			&item.Error,
			&item.RequestedAt,
			&item.CompletedAt,
			&item.PromptText,
		); err != nil {
			return nil, err
		}
		results = append(results, item)
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
		nullableJSON{&result.RawResponse},
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
