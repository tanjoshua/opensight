package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"opensight/internal/domain"
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

type resultStore interface {
	ListResults(ctx context.Context, tenantID, businessID domain.ID, filter store.ResultFilter) ([]store.PromptResult, error)
	GetResultDetail(ctx context.Context, tenantID, resultID domain.ID) (store.ResultDetail, error)
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
	Prompt       *promptResponse `json:"prompt,omitempty"`
	Run          *runResponse    `json:"run,omitempty"`
}

type promptResponse struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		s.writeInternalError(w, "list runs: store missing", errors.New("run store is required"))
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

	runs, err := s.runs.ListRuns(r.Context(), su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	resp := runsResponse{Runs: make([]runResponse, 0, len(runs))}
	for _, run := range runs {
		resp.Runs = append(resp.Runs, runToResponse(run))
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
	for _, result := range results {
		resp.Results = append(resp.Results, resultToResponse(result, false))
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

	detail, err := s.results.GetResultDetail(r.Context(), su.TenantID, resultID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	includeRaw := r.URL.Query().Get("include_raw") == "true"
	resp := resultToResponse(detail.Result, includeRaw)
	resp.Prompt = &promptResponse{
		ID:   detail.Prompt.ID.String(),
		Text: detail.Prompt.Text,
	}
	run := runToResponse(detail.Run)
	resp.Run = &run
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
