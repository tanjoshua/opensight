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
// Analyzed is true when the result has a result_analyses row; a succeeded result
// without one is the "not yet analyzed" badge state (design 06, MET-5).
type ResultListItem struct {
	PromptResult
	PromptText string
	Analyzed   bool
}

// ResultFilter narrows ListResults. All predicates are optional; Limit/Offset
// apply as given when > 0 (the handler chooses defaults and caps). Mentioned,
// when set, keeps only results whose analysis found (true) or did not find
// (false) a self mention — the Responses "mentioned" filter (design 06, MET-5).
type ResultFilter struct {
	RunID     *domain.ID
	PromptID  *domain.ID
	Status    *ResultStatus
	Mentioned *bool
	Limit     int
	Offset    int
}

// ResultMention is one mention row for the Response drawer: the self/competitor
// occurrence with its match method, first-appearance order, and evidence excerpt
// (design 06, MET-5). Competitor identity is deliberately omitted — the drawer
// highlights occurrences, it does not re-list competitors.
type ResultMention struct {
	Subject      string
	MatchedBy    string
	MentionOrder int
	Excerpt      string
}

// ResultCitation is one citation row for the Response drawer: the cited source
// with its inferred subject and first-appearance order (design 06, MET-5). The
// inline-marker annotation span is reconstructed by the API layer from
// raw_response, not stored here.
type ResultCitation struct {
	URL       string
	Domain    string
	Title     *string
	CiteOrder int
	Subject   string
}

// ResultAnalysis is the derived-analysis enrichment for one result's drawer
// (design 06, MET-5). Analyzed is false for a succeeded-but-unanalyzed result —
// no result_analyses row — in which case the other fields are empty. Sentiment
// is nil when the business was not mentioned even though the result was analyzed.
type ResultAnalysis struct {
	Analyzed  bool
	Sentiment *string
	Keywords  []string
	Excerpts  []string
	Mentions  []ResultMention
	Citations []ResultCitation
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

	// The three enrichment reads below are tenant-scoped through the same
	// prompt_results -> monitoring_runs -> businesses join the deletes use, so a
	// cross-tenant result id yields no rows rather than leaking another tenant's
	// analysis. keywords (text[]) is read as JSON via to_jsonb (see stringSlice);
	// excerpts is already jsonb.
	getResultAnalysisRowSQL = `
SELECT ra.sentiment, to_jsonb(ra.keywords) AS keywords, ra.excerpts
FROM result_analyses ra
JOIN prompt_results pr ON pr.id = ra.prompt_result_id
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
WHERE ra.prompt_result_id = $1 AND b.tenant_id = $2`

	listResultMentionsSQL = `
SELECT m.subject, m.matched_by, m.mention_order, m.excerpt
FROM mentions m
JOIN prompt_results pr ON pr.id = m.prompt_result_id
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
WHERE m.prompt_result_id = $1 AND b.tenant_id = $2
ORDER BY m.mention_order, m.id`

	listResultCitationsSQL = `
SELECT c.url, c.domain, c.title, c.cite_order, c.subject
FROM citations c
JOIN prompt_results pr ON pr.id = c.prompt_result_id
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
WHERE c.prompt_result_id = $1 AND b.tenant_id = $2
ORDER BY c.cite_order, c.id`
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

// GetResultAnalysis loads the derived-analysis enrichment for one result — its
// result_analyses row (if any), mentions, and citations — for the Response
// drawer (design 06, MET-5). All reads are tenant-scoped, so a missing or
// cross-tenant result returns an empty, unanalyzed ResultAnalysis rather than
// leaking. Ownership and existence of the result itself are proven by the
// caller's GetResultDetail; this method only fetches the child rows.
func (s *ResultStore) GetResultAnalysis(ctx context.Context, tenantID, resultID domain.ID) (ResultAnalysis, error) {
	if s == nil || s.db == nil {
		return ResultAnalysis{}, errors.New("result store database is required")
	}

	var out ResultAnalysis
	var keywords, excerpts stringSlice
	err := s.db.QueryRowContext(ctx, getResultAnalysisRowSQL, resultID, tenantID).Scan(&out.Sentiment, &keywords, &excerpts)
	switch {
	case err == nil:
		out.Analyzed = true
		out.Keywords = emptyIfNil(keywords)
		out.Excerpts = emptyIfNil(excerpts)
	case errors.Is(err, sql.ErrNoRows):
		// Succeeded-but-unanalyzed (or failed): no analysis row. Not an error.
	default:
		return ResultAnalysis{}, fmt.Errorf("get result analysis: %w", err)
	}

	mentions, err := s.listResultMentions(ctx, tenantID, resultID)
	if err != nil {
		return ResultAnalysis{}, err
	}
	out.Mentions = mentions

	citations, err := s.listResultCitations(ctx, tenantID, resultID)
	if err != nil {
		return ResultAnalysis{}, err
	}
	out.Citations = citations
	return out, nil
}

func (s *ResultStore) listResultMentions(ctx context.Context, tenantID, resultID domain.ID) ([]ResultMention, error) {
	rows, err := s.db.QueryContext(ctx, listResultMentionsSQL, resultID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list result mentions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	mentions := []ResultMention{}
	for rows.Next() {
		var m ResultMention
		if err := rows.Scan(&m.Subject, &m.MatchedBy, &m.MentionOrder, &m.Excerpt); err != nil {
			return nil, fmt.Errorf("scan mention: %w", err)
		}
		mentions = append(mentions, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mentions: %w", err)
	}
	return mentions, nil
}

func (s *ResultStore) listResultCitations(ctx context.Context, tenantID, resultID domain.ID) ([]ResultCitation, error) {
	rows, err := s.db.QueryContext(ctx, listResultCitationsSQL, resultID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list result citations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	citations := []ResultCitation{}
	for rows.Next() {
		var c ResultCitation
		if err := rows.Scan(&c.URL, &c.Domain, &c.Title, &c.CiteOrder, &c.Subject); err != nil {
			return nil, fmt.Errorf("scan citation: %w", err)
		}
		citations = append(citations, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate citations: %w", err)
	}
	return citations, nil
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
SELECT ` + resultColumns + `, p.text,
       EXISTS(SELECT 1 FROM result_analyses ra WHERE ra.prompt_result_id = pr.id) AS analyzed
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN prompts p ON p.id = pr.prompt_id AND p.business_id = r.business_id
WHERE r.business_id = $1
  AND ($2::uuid IS NULL OR pr.run_id = $2)
  AND ($3::uuid IS NULL OR pr.prompt_id = $3)
  AND ($4::text IS NULL OR pr.status = $4)
  AND ($5::boolean IS NULL OR EXISTS(
        SELECT 1 FROM mentions m WHERE m.prompt_result_id = pr.id AND m.subject = 'self') = $5)
ORDER BY pr.requested_at DESC`

	args := []any{businessID, filter.RunID, filter.PromptID, statusArg(filter.Status), boolArg(filter.Mentioned)}
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
			&item.Analyzed,
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

func boolArg(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

// emptyIfNil normalizes a nil slice to a non-nil empty one so the API encodes
// [] rather than null for a present-but-empty analysis field.
func emptyIfNil(s stringSlice) []string {
	if s == nil {
		return []string{}
	}
	return s
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
