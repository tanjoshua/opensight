package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/metrics"
	"opensight/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	defaultResultLimit = 50
	maxResultLimit     = 100
)

type runStore interface {
	ListRuns(ctx context.Context, tenantID, businessID domain.ID) ([]store.Run, error)
}

// runsMetrics is the metrics seam for the Runs endpoint: per-run visibility %
// comes from the same shared analyzed base as Overview (MET-1), so a run's
// visibility can never disagree with the trend line.
type runsMetrics interface {
	VisibilityTrend(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.VisibilityPoint, error)
}

type resultStore interface {
	ListResults(ctx context.Context, tenantID, businessID domain.ID, filter store.ResultFilter) ([]store.ResultListItem, error)
	GetResultDetail(ctx context.Context, tenantID, resultID domain.ID) (store.ResultDetail, error)
	GetResultAnalysis(ctx context.Context, tenantID, resultID domain.ID) (store.ResultAnalysis, error)
}

type runsResponse struct {
	Runs []runResponse `json:"runs"`
}

type runResponse struct {
	ID                  string  `json:"id"`
	BusinessID          string  `json:"business_id"`
	Platform            string  `json:"platform"`
	Trigger             string  `json:"trigger"`
	ScheduledFor        string  `json:"scheduled_for"`
	Status              string  `json:"status"`
	WorkflowID          string  `json:"workflow_id"`
	StartedAt           string  `json:"started_at"`
	CompletedAt         *string `json:"completed_at"`
	AnalysisCompletedAt *string `json:"analysis_completed_at"`
	// Visibility is the run's visibility % (self-mentions ÷ analyzed results). It
	// is nil for a run with no analyzed results — an unanalyzed run has no
	// visibility, distinct from 0%. Only the Runs list populates it.
	Visibility *float64 `json:"visibility"`
}

type resultsResponse struct {
	Results []resultResponse `json:"results"`
	Paging  pagingResponse   `json:"paging"`
}

type pagingResponse struct {
	Limit     int `json:"limit"`
	Offset    int `json:"offset"`
	PageCount int `json:"page_count"`
}

type resultResponse struct {
	ID           string          `json:"id"`
	RunID        string          `json:"run_id"`
	PromptID     string          `json:"prompt_id"`
	Status       string          `json:"status"`
	Model        *string         `json:"model"`
	Request      json.RawMessage `json:"request,omitempty"`
	RawResponse  json.RawMessage `json:"raw_response,omitempty"`
	ResponseText *string         `json:"response_text"`
	Error        *string         `json:"error"`
	RequestedAt  string          `json:"requested_at"`
	CompletedAt  string          `json:"completed_at"`
	// Unanalyzed flags a succeeded result with no analysis row — the "not yet
	// analyzed" badge (design 06); always false for failed results.
	Unanalyzed bool                    `json:"unanalyzed"`
	Prompt     *promptResponse         `json:"prompt,omitempty"`
	Run        *runResponse            `json:"run,omitempty"`
	Analysis   *resultAnalysisResponse `json:"analysis,omitempty"`
}

type promptResponse struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// resultAnalysisResponse is the Response drawer's evidence payload (design 06,
// MET-5): the analyzed result's mentions, sentiment + supporting excerpts,
// keywords, and citations. Present only on GET /results/:id, and only when the
// result was analyzed. Sentiment is nil when the business was not mentioned.
type resultAnalysisResponse struct {
	Sentiment *string            `json:"sentiment"`
	Keywords  []string           `json:"keywords"`
	Excerpts  []string           `json:"excerpts"`
	Mentions  []mentionResponse  `json:"mentions"`
	Citations []citationResponse `json:"citations"`
}

type mentionResponse struct {
	Subject      string `json:"subject"`
	VerbatimName string `json:"verbatim_name"`
	Order        int    `json:"order"`
	MatchedBy    string `json:"matched_by"`
	Excerpt      string `json:"excerpt"`
}

// citationResponse carries the cited source plus its annotation span — the
// character range in response_text where the inline marker renders (design 06).
// Span is nil when it cannot be resolved from raw_response (e.g. legacy rows).
type citationResponse struct {
	URL       string        `json:"url"`
	Domain    string        `json:"domain"`
	Title     *string       `json:"title"`
	CiteOrder int           `json:"cite_order"`
	Subject   string        `json:"subject"`
	Span      *spanResponse `json:"span"`
}

// spanResponse is a [start, end) text index range into response_text, taken from
// the response's url_citation annotation (design 06 "annotation spans").
type spanResponse struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil || s.runMetrics == nil {
		s.writeInternalError(w, "list runs: store missing", errors.New("run store and metrics are required"))
		return
	}

	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "list runs: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}

	ctx := r.Context()
	runs, err := s.runs.ListRuns(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Per-run visibility % from the same analyzed base as the trend line; runs
	// with no analyzed results simply have no point, so their visibility stays nil.
	points, err := s.runMetrics.VisibilityTrend(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	visibility := make(map[domain.ID]float64, len(points))
	for _, p := range points {
		visibility[p.RunID] = p.Percent
	}

	resp := runsResponse{Runs: make([]runResponse, 0, len(runs))}
	for _, run := range runs {
		row := runToResponse(run)
		if pct, ok := visibility[run.ID]; ok {
			row.Visibility = &pct
		}
		resp.Runs = append(resp.Runs, row)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListResults(w http.ResponseWriter, r *http.Request) {
	if s.results == nil {
		s.writeInternalError(w, "list results: store missing", errors.New("result store is required"))
		return
	}

	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "list results: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}

	filter, limit, offset, ok := resultFilterFromRequest(w, r)
	if !ok {
		return
	}
	results, err := s.results.ListResults(r.Context(), su.TenantID, businessID, filter)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	resp := resultsResponse{
		Results: make([]resultResponse, 0, len(results)),
		Paging:  pagingResponse{Limit: limit, Offset: offset, PageCount: len(results)},
	}
	for _, item := range results {
		row := resultToResponse(item.PromptResult, false)
		row.Unanalyzed = isUnanalyzed(item.Status, item.Analyzed)
		row.Prompt = &promptResponse{ID: item.PromptID.String(), Text: item.PromptText}
		resp.Results = append(resp.Results, row)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetResult(w http.ResponseWriter, r *http.Request) {
	if s.results == nil {
		s.writeInternalError(w, "get result: store missing", errors.New("result store is required"))
		return
	}

	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "get result: missing session context", errors.New("missing session context"))
		return
	}
	resultID, ok := pathID(w, r, "resultID")
	if !ok {
		return
	}

	ctx := r.Context()
	detail, err := s.results.GetResultDetail(ctx, su.TenantID, resultID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	analysis, err := s.results.GetResultAnalysis(ctx, su.TenantID, resultID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	includeRaw := r.URL.Query().Get("include_raw") == "true"
	resp := resultToResponse(detail.Result, includeRaw)
	resp.Unanalyzed = isUnanalyzed(detail.Result.Status, analysis.Analyzed)
	resp.Prompt = &promptResponse{
		ID:   detail.Prompt.ID.String(),
		Text: detail.Prompt.Text,
	}
	run := runToResponse(detail.Run)
	resp.Run = &run
	if analysis.Analyzed {
		resp.Analysis = analysisToResponse(analysis, detail.Result.RawResponse)
	}
	writeJSON(w, http.StatusOK, resp)
}

func resultFilterFromRequest(w http.ResponseWriter, r *http.Request) (store.ResultFilter, int, int, bool) {
	q := r.URL.Query()
	limit, ok := positiveIntParam(w, q.Get("limit"), defaultResultLimit, maxResultLimit, "limit")
	if !ok {
		return store.ResultFilter{}, 0, 0, false
	}
	offset, ok := nonNegativeIntParam(w, q.Get("offset"), 0, "offset")
	if !ok {
		return store.ResultFilter{}, 0, 0, false
	}

	filter := store.ResultFilter{Limit: limit, Offset: offset}
	if raw := q.Get("run"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "bad request", "run must be a UUID")
			return store.ResultFilter{}, 0, 0, false
		}
		filter.RunID = &id
	}
	if raw := q.Get("prompt"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "bad request", "prompt must be a UUID")
			return store.ResultFilter{}, 0, 0, false
		}
		filter.PromptID = &id
	}
	if raw := q.Get("status"); raw != "" {
		status := store.ResultStatus(raw)
		switch status {
		case store.ResultStatusSucceeded, store.ResultStatusFailed:
			filter.Status = &status
		default:
			writeProblem(w, http.StatusBadRequest, "bad request", "status must be succeeded or failed")
			return store.ResultFilter{}, 0, 0, false
		}
	}
	if raw := q.Get("mentioned"); raw != "" {
		mentioned, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "bad request", "mentioned must be true or false")
			return store.ResultFilter{}, 0, 0, false
		}
		filter.Mentioned = &mentioned
	}

	return filter, limit, offset, true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (domain.ID, bool) {
	raw := chi.URLParam(r, name)
	id, err := uuid.Parse(raw)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "bad request", fmt.Sprintf("%s must be a UUID", name))
		return uuid.Nil, false
	}
	return id, true
}

func positiveIntParam(w http.ResponseWriter, raw string, fallback, max int, name string) (int, bool) {
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		writeProblem(w, http.StatusBadRequest, "bad request", name+" must be a positive integer")
		return 0, false
	}
	if value > max {
		value = max
	}
	return value, true
}

func nonNegativeIntParam(w http.ResponseWriter, raw string, fallback int, name string) (int, bool) {
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		writeProblem(w, http.StatusBadRequest, "bad request", name+" must be a non-negative integer")
		return 0, false
	}
	return value, true
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "not found", "not found")
		return
	}
	slog.Error("api: store error", "error", err)
	writeProblem(w, http.StatusInternalServerError, "internal server error", "an unexpected error occurred")
}

func resultToResponse(result store.PromptResult, includeRaw bool) resultResponse {
	resp := resultResponse{
		ID:           result.ID.String(),
		RunID:        result.RunID.String(),
		PromptID:     result.PromptID.String(),
		Status:       string(result.Status),
		Model:        result.Model,
		Request:      result.Request,
		ResponseText: result.ResponseText,
		Error:        result.Error,
		RequestedAt:  formatTime(result.RequestedAt),
		CompletedAt:  formatTime(result.CompletedAt),
	}
	if includeRaw {
		resp.RawResponse = result.RawResponse
	}
	return resp
}

// isUnanalyzed is the "not yet analyzed" badge rule: a succeeded result with no
// analysis row. Failed results are never flagged this way — they show an error.
func isUnanalyzed(status store.ResultStatus, analyzed bool) bool {
	return status == store.ResultStatusSucceeded && !analyzed
}

// analysisToResponse shapes the drawer evidence and reconstructs each citation's
// annotation span from raw_response. buildCitationWrites (ANA-2) writes one
// citation row per url_citation annotation in StartIndex order with cite_order =
// that index, so the stored cite_order indexes straight back into the
// StartIndex-sorted annotations — no URL matching needed. A raw_response that is
// missing or shorter than expected simply leaves later spans nil.
func analysisToResponse(a store.ResultAnalysis, rawResponse json.RawMessage) *resultAnalysisResponse {
	resp := &resultAnalysisResponse{
		Sentiment: a.Sentiment,
		Keywords:  a.Keywords,
		Excerpts:  a.Excerpts,
		Mentions:  make([]mentionResponse, 0, len(a.Mentions)),
		Citations: make([]citationResponse, 0, len(a.Citations)),
	}
	for _, m := range a.Mentions {
		resp.Mentions = append(resp.Mentions, mentionResponse{
			Subject:      m.Subject,
			VerbatimName: m.VerbatimName,
			Order:        m.MentionOrder,
			MatchedBy:    m.MatchedBy,
			Excerpt:      m.Excerpt,
		})
	}

	spans := citationSpans(rawResponse)
	for _, c := range a.Citations {
		row := citationResponse{
			URL:       c.URL,
			Domain:    c.Domain,
			Title:     c.Title,
			CiteOrder: c.CiteOrder,
			Subject:   c.Subject,
		}
		if c.CiteOrder >= 0 && c.CiteOrder < len(spans) {
			row.Span = &spans[c.CiteOrder]
		}
		resp.Citations = append(resp.Citations, row)
	}
	return resp
}

// citationSpans returns the response's url_citation annotation spans in
// first-appearance (StartIndex-ascending) order — the same order cite_order was
// assigned in. A malformed or empty raw_response yields no spans.
func citationSpans(rawResponse json.RawMessage) []spanResponse {
	annotations, err := llm.ParseCitationAnnotations(rawResponse)
	if err != nil || len(annotations) == 0 {
		return nil
	}
	sort.SliceStable(annotations, func(i, j int) bool {
		return annotations[i].StartIndex < annotations[j].StartIndex
	})
	spans := make([]spanResponse, len(annotations))
	for i, a := range annotations {
		spans[i] = spanResponse{Start: a.StartIndex, End: a.EndIndex}
	}
	return spans
}

func runToResponse(run store.Run) runResponse {
	return runResponse{
		ID:                  run.ID.String(),
		BusinessID:          run.BusinessID.String(),
		Platform:            run.Platform,
		Trigger:             string(run.Trigger),
		ScheduledFor:        run.ScheduledFor.Format(time.DateOnly),
		Status:              string(run.Status),
		WorkflowID:          run.WorkflowID,
		StartedAt:           formatTime(run.StartedAt),
		CompletedAt:         formatOptionalTime(run.CompletedAt),
		AnalysisCompletedAt: formatOptionalTime(run.AnalysisCompletedAt),
	}
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func formatOptionalTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := formatTime(*t)
	return &formatted
}
