package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"opensight/internal/domain"
	"opensight/internal/metrics"
	"opensight/internal/store"
)

const (
	defaultCompetitorLimit = 50
	maxCompetitorLimit     = 100
)

// competitorsMetrics is the metrics seam for the Competitors section (MET-4).
// CompetitorStats is tenant-scoped and computes over the shared analyzed base
// (MET-1), so a competitor's mention % and the business's own visibility % are
// always measured against the identical result set.
type competitorsMetrics interface {
	CompetitorStats(ctx context.Context, tenantID, businessID domain.ID) (metrics.CompetitorStats, error)
}

type competitorStore interface {
	CreateManual(ctx context.Context, params store.CreateManualCompetitorParams) (store.CompetitorRecord, error)
	SetStatus(ctx context.Context, params store.SetCompetitorStatusParams) (store.CompetitorRecord, error)
	ApproveSuggestedAlias(ctx context.Context, params store.SuggestedAliasParams) (store.CompetitorRecord, error)
	RejectSuggestedAlias(ctx context.Context, params store.SuggestedAliasParams) (store.CompetitorRecord, error)
	UpdateAliases(ctx context.Context, params store.UpdateCompetitorAliasesParams) (store.CompetitorRecord, error)
}

type competitorsListResponse struct {
	Self        competitorSelfResponse `json:"self"`
	Competitors []competitorResponse   `json:"competitors"`
	Paging      pagingResponse         `json:"paging"`
}

// competitorSelfResponse is the business's own coverage over the shared analyzed
// base — the baseline each competitor's VsSelf is measured against.
type competitorSelfResponse struct {
	TotalAnalyzed int      `json:"total_analyzed"`
	Mentioned     int      `json:"mentioned"`
	Percent       float64  `json:"percent"`
	ResultIDs     []string `json:"result_ids"`
}

// competitorResponse is one competitor's full comparison stats (design 06/PRD §6):
// mention %, totals, avg order, per-prompt appearances, weekly trend, vs-self.
// ResultIDs (and every nested ResultIDs) is the door behind the number.
type competitorResponse struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Status           string                     `json:"status"`
	Aliases          []string                   `json:"aliases"`
	SuggestedAliases []string                   `json:"suggested_aliases"`
	Mentioned        int                        `json:"mentioned"`
	TotalMentions    int                        `json:"total_mentions"`
	MentionPercent   float64                    `json:"mention_percent"`
	AvgOrder         float64                    `json:"avg_order"`
	VsSelf           float64                    `json:"vs_self"`
	ResultIDs        []string                   `json:"result_ids"`
	PerPrompt        []competitorPromptResponse `json:"per_prompt"`
	Trend            []competitorTrendResponse  `json:"trend"`
}

type competitorWriteResponse struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Website          *string  `json:"website"`
	Aliases          []string `json:"aliases"`
	SuggestedAliases []string `json:"suggested_aliases"`
	Source           string   `json:"source"`
	Status           string   `json:"status"`
}

type addCompetitorRequest struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	Website string   `json:"website"`
}

type suggestedAliasRequest struct {
	Alias string `json:"alias"`
}

type patchCompetitorRequest struct {
	Aliases *[]string `json:"aliases"`
}

// competitorPromptResponse is "in N of the responses to this prompt": the prompt
// and the analyzed results in which the competitor appeared.
type competitorPromptResponse struct {
	PromptID   string   `json:"prompt_id"`
	PromptText string   `json:"prompt_text"`
	ResultIDs  []string `json:"result_ids"`
}

// competitorTrendResponse is a competitor's mention % for one analyzed run, over
// the same per-run denominator as the business's own visibility that week.
type competitorTrendResponse struct {
	RunID        string   `json:"run_id"`
	ScheduledFor string   `json:"scheduled_for"`
	Analyzed     int      `json:"analyzed"`
	Mentioned    int      `json:"mentioned"`
	Percent      float64  `json:"percent"`
	ResultIDs    []string `json:"result_ids"`
}

// handleListCompetitors serves GET /businesses/:id/competitors (MET-4): every
// competitor's comparison stats, coverage-ranked, with an optional ?status
// filter and limit/offset pagination. GetBusiness is the ownership gate — it
// returns ErrNotFound for a missing or cross-tenant business, so CompetitorStats
// (which returns empty for an unowned business) only runs once ownership is
// proven. Status is a display filter: dismissed competitors keep their full
// history and are returned as-is when asked for (design 02/05).
func (s *Server) handleListCompetitors(w http.ResponseWriter, r *http.Request) {
	if s.businesses == nil || s.competitorMetrics == nil {
		s.writeInternalError(w, "list competitors: store missing", errors.New("business store and metrics are required"))
		return
	}

	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "list competitors: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}
	status, ok := competitorStatusParam(w, r)
	if !ok {
		return
	}
	limit, ok := positiveIntParam(w, r.URL.Query().Get("limit"), defaultCompetitorLimit, maxCompetitorLimit, "limit")
	if !ok {
		return
	}
	offset, ok := nonNegativeIntParam(w, r.URL.Query().Get("offset"), 0, "offset")
	if !ok {
		return
	}

	ctx := r.Context()
	if _, err := s.businesses.GetBusiness(ctx, su.TenantID, businessID); err != nil {
		writeStoreError(w, err)
		return
	}

	stats, err := s.competitorMetrics.CompetitorStats(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Filter by status (display filter), then page. CompetitorStats is already
	// coverage-desc, so the filtered slice keeps that ranking.
	filtered := stats.Competitors
	if status != "" {
		filtered = filtered[:0:0]
		for _, c := range stats.Competitors {
			if c.Status == status {
				filtered = append(filtered, c)
			}
		}
	}
	start := min(offset, len(filtered))
	end := start + min(limit, len(filtered)-start)
	page := filtered[start:end]

	resp := competitorsListResponse{
		Self: competitorSelfResponse{
			TotalAnalyzed: stats.TotalAnalyzed,
			Mentioned:     stats.SelfMentioned,
			Percent:       stats.SelfPercent,
			ResultIDs:     idStrings(stats.ResultIDs),
		},
		Competitors: make([]competitorResponse, 0, len(page)),
		Paging:      pagingResponse{Limit: limit, Offset: offset, PageCount: len(page)},
	}
	for _, c := range page {
		resp.Competitors = append(resp.Competitors, competitorToResponse(c))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAddCompetitor(w http.ResponseWriter, r *http.Request) {
	if s.competitors == nil {
		s.writeInternalError(w, "add competitor: store missing", errors.New("competitor store is required"))
		return
	}
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "add competitor: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req addCompetitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "bad request", "request body must be valid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeProblem(w, http.StatusBadRequest, "bad request", "name is required")
		return
	}
	var website *string
	if value := strings.TrimSpace(req.Website); value != "" {
		website = &value
	}
	competitor, err := s.competitors.CreateManual(r.Context(), store.CreateManualCompetitorParams{
		TenantID:   su.TenantID,
		BusinessID: businessID,
		Name:       req.Name,
		Aliases:    req.Aliases,
		Website:    website,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeStoreError(w, err)
		} else {
			s.writeInternalError(w, "add competitor: create manual competitor", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, competitorRecordToResponse(competitor))
}

func (s *Server) handleTrackCompetitor(w http.ResponseWriter, r *http.Request) {
	s.handleSetCompetitorStatus(w, r, store.CompetitorStatusTracked)
}

func (s *Server) handleDismissCompetitor(w http.ResponseWriter, r *http.Request) {
	s.handleSetCompetitorStatus(w, r, store.CompetitorStatusDismissed)
}

func (s *Server) handleSetCompetitorStatus(w http.ResponseWriter, r *http.Request, status store.CompetitorStatus) {
	if s.competitors == nil {
		s.writeInternalError(w, "set competitor status: store missing", errors.New("competitor store is required"))
		return
	}
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "set competitor status: missing session context", errors.New("missing session context"))
		return
	}
	competitorID, ok := pathID(w, r, "competitorID")
	if !ok {
		return
	}
	competitor, err := s.competitors.SetStatus(r.Context(), store.SetCompetitorStatusParams{
		TenantID:     su.TenantID,
		CompetitorID: competitorID,
		Status:       status,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, competitorRecordToResponse(competitor))
}

func (s *Server) handleApproveSuggestedAlias(w http.ResponseWriter, r *http.Request) {
	s.handleSuggestedAlias(w, r, true)
}

func (s *Server) handleRejectSuggestedAlias(w http.ResponseWriter, r *http.Request) {
	s.handleSuggestedAlias(w, r, false)
}

func (s *Server) handleSuggestedAlias(w http.ResponseWriter, r *http.Request, approve bool) {
	if s.competitors == nil {
		s.writeInternalError(w, "review suggested alias: store missing", errors.New("competitor store is required"))
		return
	}
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "review suggested alias: missing session context", errors.New("missing session context"))
		return
	}
	competitorID, ok := pathID(w, r, "competitorID")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req suggestedAliasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "bad request", "request body must be valid JSON")
		return
	}
	alias := strings.TrimSpace(req.Alias)
	if alias == "" {
		writeProblem(w, http.StatusBadRequest, "bad request", "alias is required")
		return
	}
	params := store.SuggestedAliasParams{
		TenantID: su.TenantID, CompetitorID: competitorID, Alias: alias,
	}
	var competitor store.CompetitorRecord
	var err error
	if approve {
		competitor, err = s.competitors.ApproveSuggestedAlias(r.Context(), params)
	} else {
		competitor, err = s.competitors.RejectSuggestedAlias(r.Context(), params)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, competitorRecordToResponse(competitor))
}

func (s *Server) handlePatchCompetitorAliases(w http.ResponseWriter, r *http.Request) {
	if s.competitors == nil {
		s.writeInternalError(w, "patch competitor aliases: store missing", errors.New("competitor store is required"))
		return
	}
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "patch competitor aliases: missing session context", errors.New("missing session context"))
		return
	}
	competitorID, ok := pathID(w, r, "competitorID")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req patchCompetitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "bad request", "request body must be valid JSON")
		return
	}
	if req.Aliases == nil {
		writeProblem(w, http.StatusBadRequest, "bad request", "aliases is required")
		return
	}
	for _, alias := range *req.Aliases {
		if strings.TrimSpace(alias) == "" {
			writeProblem(w, http.StatusBadRequest, "bad request", "aliases must not contain blank values")
			return
		}
	}
	record, err := s.competitors.UpdateAliases(r.Context(), store.UpdateCompetitorAliasesParams{
		TenantID: su.TenantID, CompetitorID: competitorID, Aliases: *req.Aliases,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, competitorRecordToResponse(record))
}

func competitorRecordToResponse(record store.CompetitorRecord) competitorWriteResponse {
	return competitorWriteResponse{
		ID:               record.ID.String(),
		Name:             record.Name,
		Website:          record.Website,
		Aliases:          record.Aliases,
		SuggestedAliases: record.SuggestedAliases,
		Source:           record.Source,
		Status:           string(record.Status),
	}
}

// competitorStatusParam reads and validates the optional ?status display filter.
// An empty value means "all statuses"; anything else must be a real status
// (design 02: discovered|tracked|dismissed).
func competitorStatusParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := r.URL.Query().Get("status")
	switch raw {
	case "", "discovered", "tracked", "dismissed":
		return raw, true
	default:
		writeProblem(w, http.StatusBadRequest, "bad request", "status must be discovered, tracked, or dismissed")
		return "", false
	}
}

func competitorToResponse(c metrics.CompetitorStat) competitorResponse {
	resp := competitorResponse{
		ID:               c.CompetitorID.String(),
		Name:             c.Name,
		Status:           c.Status,
		Aliases:          c.Aliases,
		SuggestedAliases: c.SuggestedAliases,
		Mentioned:        c.Mentioned,
		TotalMentions:    c.TotalMentions,
		MentionPercent:   c.MentionPercent,
		AvgOrder:         c.AvgOrder,
		VsSelf:           c.VsSelf,
		ResultIDs:        idStrings(c.ResultIDs),
		PerPrompt:        make([]competitorPromptResponse, 0, len(c.PerPrompt)),
		Trend:            competitorTrendToResponse(c.Trend),
	}
	for _, p := range c.PerPrompt {
		resp.PerPrompt = append(resp.PerPrompt, competitorPromptResponse{
			PromptID:   p.PromptID.String(),
			PromptText: p.Text,
			ResultIDs:  idStrings(p.ResultIDs),
		})
	}
	return resp
}

// competitorTrendToResponse shapes a competitor's weekly mention-% series. It is
// shared by the Competitors list and the Overview chart so both plot the same
// per-run trend the metrics layer computes (MET-4).
func competitorTrendToResponse(points []metrics.CompetitorTrendPoint) []competitorTrendResponse {
	out := make([]competitorTrendResponse, 0, len(points))
	for _, t := range points {
		out = append(out, competitorTrendResponse{
			RunID:        t.RunID.String(),
			ScheduledFor: t.ScheduledFor.Format(time.DateOnly),
			Analyzed:     t.Analyzed,
			Mentioned:    t.Mentioned,
			Percent:      t.Percent,
			ResultIDs:    idStrings(t.ResultIDs),
		})
	}
	return out
}
