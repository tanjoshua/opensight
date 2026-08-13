package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicateResult is returned when a prompt result already exists for a
// (run, prompt) pair. The store never silently absorbs the conflict: the
// ExecutePrompt's retry path re-gets the existing row on this error.
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
// without one is the "not yet analyzed" badge state (design 06).
// SelfMentioned reports whether the analysis found a self mention. It is only
// meaningful when Analyzed — an unanalyzed result has no mentions rows yet, so
// it reads false for the same reason a genuinely absent business does, and
// callers must gate on Analyzed before showing it.
type ResultListItem struct {
	PromptResult
	PromptText    string
	Analyzed      bool
	SelfMentioned bool
}

// ResultFilter narrows ListResults. All predicates are optional; Limit/Offset
// apply as given when > 0 (the handler chooses defaults and caps). ResultIDs,
// when non-empty, restrict results and preserve the requested order. Mentioned,
// when set, keeps only results whose analysis found (true) or did not find
// (false) a self mention — the Responses "mentioned" filter (design 06).
type ResultFilter struct {
	ResultIDs []domain.ID
	RunID     *domain.ID
	PromptID  *domain.ID
	Status    *ResultStatus
	Mentioned *bool
	Limit     int
	Offset    int
}

// ResultMention is one mention row for the Response drawer: the self/competitor
// occurrence with its exact extracted name, match method, first-appearance
// order, and evidence excerpt (design 06). Competitor identity is
// deliberately omitted — the drawer highlights occurrences, it does not re-list
// competitors.
type ResultMention struct {
	Subject      string
	VerbatimName string
	MatchedBy    string
	MentionOrder int
	Excerpt      string
}

// ResultCitation is one citation row for the Response drawer: the cited source
// with its inferred subject and first-appearance order (design 06). The
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
// (design 06). Analyzed is false for a succeeded-but-unanalyzed result —
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

// CreateResult appends a prompt result, scoped by a account-predicated lookup of
// the run's business inside one transaction. A missing or cross-account run
// returns ErrNotFound; a UNIQUE (run_id, prompt_id) violation returns
// ErrDuplicateResult. ExecutePrompt's retry re-gets the existing row on the
// latter (get -> miss -> create -> on ErrDuplicateResult re-get).
func (s *Store) CreateResult(ctx context.Context, accountID domain.ID, params CreateResultParams) (PromptResult, error) {

	params, err := normalizeCreateResultParams(params)
	if err != nil {
		return PromptResult{}, err
	}
	if err := validateUUIDv7("account id", accountID); err != nil {
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
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		if _, err := q.RunPromptOwned(ctx, storesqlc.RunPromptOwnedParams{
			ID: params.RunID, PromptID: params.PromptID, AccountID: accountID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("verify run/prompt ownership: %w", err)
		}
		var rawResponse *json.RawMessage
		if len(params.RawResponse) > 0 {
			rawResponse = &params.RawResponse
		}
		times, err := q.InsertResult(ctx, storesqlc.InsertResultParams{
			ID: params.ID, RunID: params.RunID, PromptID: params.PromptID, Status: string(params.Status),
			Model: params.Model, Request: params.Request, RawResponse: rawResponse,
			ResponseText: params.ResponseText, Error: params.Error,
			RequestedAt: nullableTime(params.RequestedAt), CompletedAt: nullableTime(params.CompletedAt),
		})
		if err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicateResult
			}
			return fmt.Errorf("insert prompt result: %w", err)
		}
		result.RequestedAt, result.CompletedAt = times.RequestedAt, times.CompletedAt
		return nil
	})
	if err != nil {
		return PromptResult{}, err
	}
	return result, nil
}

// GetResultByRunAndPrompt loads the result for a (run, prompt) pair, account
// scoped via the business join (deep-by-id). No row returns ErrNotFound.
// ExecutePrompt calls this first for its idempotency check and after an
// ErrDuplicateResult race.
func (s *Store) GetResultByRunAndPrompt(ctx context.Context, accountID, runID, promptID domain.ID) (PromptResult, error) {

	row, err := s.q(ctx).GetResultByRunAndPrompt(ctx, storesqlc.GetResultByRunAndPromptParams{
		RunID: runID, PromptID: promptID, AccountID: accountID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PromptResult{}, ErrNotFound
		}
		return PromptResult{}, fmt.Errorf("get result by run and prompt: %w", err)
	}
	return resultFromSQLC(row), nil
}

// GetResult loads a single result by id, account scoped via
// prompt_results -> monitoring_runs -> businesses. A missing or cross-account
// result returns ErrNotFound.
func (s *Store) GetResult(ctx context.Context, accountID, resultID domain.ID) (PromptResult, error) {

	row, err := s.q(ctx).GetResult(ctx, storesqlc.GetResultParams{ID: resultID, AccountID: accountID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PromptResult{}, ErrNotFound
		}
		return PromptResult{}, fmt.Errorf("get result: %w", err)
	}
	return resultFromSQLC(row), nil
}

// GetResultDetail loads one result with its prompt text and run metadata,
// account scoped through prompt_results -> monitoring_runs -> businesses.
func (s *Store) GetResultDetail(ctx context.Context, accountID, resultID domain.ID) (ResultDetail, error) {

	row, err := s.q(ctx).GetResultDetail(ctx, storesqlc.GetResultDetailParams{ID: resultID, AccountID: accountID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResultDetail{}, ErrNotFound
		}
		return ResultDetail{}, fmt.Errorf("get result detail: %w", err)
	}

	detail := ResultDetail{
		Result: resultFromFields(row.ID, row.RunID, row.PromptID, row.Status, row.Model, row.Request,
			row.RawResponse, row.ResponseText, row.Error, row.RequestedAt, row.CompletedAt),
		BusinessID: row.BusinessID,
		Run: runFromFields(row.RunID, row.BusinessID, row.Platform, row.Trigger, row.ScheduledFor,
			row.RunStatus, row.JobID, row.StartedAt, row.RunCompletedAt, row.AnalysisCompletedAt, nil),
		Prompt: Prompt{ID: row.PromptID, BusinessID: row.BusinessID, Text: row.Text},
	}
	return detail, nil
}

// GetResultAnalysis loads the derived-analysis enrichment for one result — its
// result_analyses row (if any), mentions, and citations — for the Response
// drawer (design 06). All reads are account-scoped, so a missing or
// cross-account result returns an empty, unanalyzed ResultAnalysis rather than
// leaking. Ownership and existence of the result itself are proven by the
// caller's GetResultDetail; this method only fetches the child rows.
func (s *Store) GetResultAnalysis(ctx context.Context, accountID, resultID domain.ID) (ResultAnalysis, error) {

	var out ResultAnalysis
	row, err := s.q(ctx).GetResultAnalysisRow(ctx, storesqlc.GetResultAnalysisRowParams{
		PromptResultID: resultID, AccountID: accountID,
	})
	switch {
	case err == nil:
		out.Analyzed = true
		out.Sentiment = row.Sentiment
		out.Keywords = emptyStrings(row.Keywords)
		if err := json.Unmarshal(row.Excerpts, &out.Excerpts); err != nil {
			return ResultAnalysis{}, fmt.Errorf("decode analysis excerpts: %w", err)
		}
	case errors.Is(err, pgx.ErrNoRows):
		// Succeeded-but-unanalyzed (or failed): no analysis row. Not an error.
	default:
		return ResultAnalysis{}, fmt.Errorf("get result analysis: %w", err)
	}

	mentions, err := s.listResultMentions(ctx, accountID, resultID)
	if err != nil {
		return ResultAnalysis{}, err
	}
	out.Mentions = mentions

	citations, err := s.listResultCitations(ctx, accountID, resultID)
	if err != nil {
		return ResultAnalysis{}, err
	}
	out.Citations = citations
	return out, nil
}

func (s *Store) listResultMentions(ctx context.Context, accountID, resultID domain.ID) ([]ResultMention, error) {
	rows, err := s.q(ctx).ListResultMentions(ctx, storesqlc.ListResultMentionsParams{
		PromptResultID: resultID, AccountID: accountID,
	})
	if err != nil {
		return nil, fmt.Errorf("list result mentions: %w", err)
	}
	mentions := make([]ResultMention, 0, len(rows))
	for _, row := range rows {
		mentions = append(mentions, ResultMention{
			Subject: row.Subject, VerbatimName: row.VerbatimName, MatchedBy: row.MatchedBy,
			MentionOrder: int(row.MentionOrder), Excerpt: row.Excerpt,
		})
	}
	return mentions, nil
}

func (s *Store) listResultCitations(ctx context.Context, accountID, resultID domain.ID) ([]ResultCitation, error) {
	rows, err := s.q(ctx).ListResultCitations(ctx, storesqlc.ListResultCitationsParams{
		PromptResultID: resultID, AccountID: accountID,
	})
	if err != nil {
		return nil, fmt.Errorf("list result citations: %w", err)
	}
	citations := make([]ResultCitation, 0, len(rows))
	for _, row := range rows {
		citations = append(citations, ResultCitation{
			URL: row.Url, Domain: row.Domain, Title: row.Title, CiteOrder: int(row.CiteOrder), Subject: row.Subject,
		})
	}
	return citations, nil
}

// ListResults returns a business's results with optional hard-coded
// predicates. It enters through the account-checked business lookup, then joins
// results up to the business so foreign run/prompt filters yield nothing rather
// than leaking across accounts.
func (s *Store) ListResults(ctx context.Context, accountID, businessID domain.ID, filter ResultFilter) ([]ResultListItem, error) {

	q := s.q(ctx)
	if err := businessOwned(ctx, q, accountID, businessID); err != nil {
		return nil, err
	}

	limit := filter.Limit
	const maxInt32 = int(^uint32(0) >> 1)
	if limit <= 0 || limit > maxInt32 {
		limit = maxInt32
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	} else if offset > maxInt32 {
		offset = maxInt32
	}
	var status *string
	if filter.Status != nil {
		value := string(*filter.Status)
		status = &value
	}
	rows, err := q.ListResults(ctx, storesqlc.ListResultsParams{
		BusinessID: businessID, RunID: filter.RunID, PromptID: filter.PromptID,
		Status: status, Mentioned: filter.Mentioned, ResultIds: filter.ResultIDs,
		ResultLimit: int32(limit), ResultOffset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("list results: %w", err)
	}
	results := make([]ResultListItem, 0, len(rows))
	for _, row := range rows {
		item := ResultListItem{
			PromptResult: resultFromFields(row.ID, row.RunID, row.PromptID, row.Status, row.Model,
				row.Request, row.RawResponse, row.ResponseText, row.Error, row.RequestedAt, row.CompletedAt),
			PromptText: row.PromptText, Analyzed: row.Analyzed,
			SelfMentioned: row.SelfMentioned,
		}
		results = append(results, item)
	}
	return results, nil
}

func resultFromSQLC(row storesqlc.PromptResult) PromptResult {
	return resultFromFields(row.ID, row.RunID, row.PromptID, row.Status, row.Model, row.Request,
		row.RawResponse, row.ResponseText, row.Error, row.RequestedAt, row.CompletedAt)
}

func resultFromFields(id, runID, promptID domain.ID, status string, model *string, request json.RawMessage,
	raw *json.RawMessage, response, resultErr *string, requested, completed time.Time) PromptResult {
	var rawResponse json.RawMessage
	if raw != nil {
		rawResponse = *raw
	}
	return PromptResult{ID: id, RunID: runID, PromptID: promptID, Status: ResultStatus(status), Model: model,
		Request: request, RawResponse: rawResponse, ResponseText: response, Error: resultErr,
		RequestedAt: requested, CompletedAt: completed}
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

// emptyStrings normalizes a nil slice to a non-nil empty one so the API encodes
// [] rather than null for a present-but-empty analysis field.
func emptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
