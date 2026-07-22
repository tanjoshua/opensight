package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"opensight/internal/domain"
	"opensight/internal/metrics"
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

type competitorsListResponse struct {
	Self        competitorSelfResponse `json:"self"`
	Competitors []competitorResponse   `json:"competitors"`
	Paging      pagingResponse         `json:"paging"`
}

// competitorSelfResponse is the business's own coverage over the shared analyzed
// base — the baseline each competitor's VsSelf is measured against.
type competitorSelfResponse struct {
	TotalAnalyzed int     `json:"total_analyzed"`
	Mentioned     int     `json:"mentioned"`
	Percent       float64 `json:"percent"`
}

// competitorResponse is one competitor's full comparison stats (design 06/PRD §6):
// mention %, totals, avg order, per-prompt appearances, weekly trend, vs-self.
// ResultIDs (and every nested ResultIDs) is the door behind the number.
type competitorResponse struct {
	ID             string                     `json:"id"`
	Name           string                     `json:"name"`
	Status         string                     `json:"status"`
	Mentioned      int                        `json:"mentioned"`
	TotalMentions  int                        `json:"total_mentions"`
	MentionPercent float64                    `json:"mention_percent"`
	AvgOrder       float64                    `json:"avg_order"`
	VsSelf         float64                    `json:"vs_self"`
	ResultIDs      []string                   `json:"result_ids"`
	PerPrompt      []competitorPromptResponse `json:"per_prompt"`
	Trend          []competitorTrendResponse  `json:"trend"`
}

// competitorPromptResponse is "in N of the responses to this prompt": the prompt
// and the analyzed results in which the competitor appeared.
type competitorPromptResponse struct {
	PromptID  string   `json:"prompt_id"`
	ResultIDs []string `json:"result_ids"`
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
	page := filtered[min(offset, len(filtered)):min(offset+limit, len(filtered))]

	resp := competitorsListResponse{
		Self: competitorSelfResponse{
			TotalAnalyzed: stats.TotalAnalyzed,
			Mentioned:     stats.SelfMentioned,
			Percent:       stats.SelfPercent,
		},
		Competitors: make([]competitorResponse, 0, len(page)),
		Paging:      pagingResponse{Limit: limit, Offset: offset, PageCount: len(page)},
	}
	for _, c := range page {
		resp.Competitors = append(resp.Competitors, competitorToResponse(c))
	}
	writeJSON(w, http.StatusOK, resp)
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
		ID:             c.CompetitorID.String(),
		Name:           c.Name,
		Status:         c.Status,
		Mentioned:      c.Mentioned,
		TotalMentions:  c.TotalMentions,
		MentionPercent: c.MentionPercent,
		AvgOrder:       c.AvgOrder,
		VsSelf:         c.VsSelf,
		ResultIDs:      idStrings(c.ResultIDs),
		PerPrompt:      make([]competitorPromptResponse, 0, len(c.PerPrompt)),
		Trend:          make([]competitorTrendResponse, 0, len(c.Trend)),
	}
	for _, p := range c.PerPrompt {
		resp.PerPrompt = append(resp.PerPrompt, competitorPromptResponse{
			PromptID:  p.PromptID.String(),
			ResultIDs: idStrings(p.ResultIDs),
		})
	}
	for _, t := range c.Trend {
		resp.Trend = append(resp.Trend, competitorTrendResponse{
			RunID:        t.RunID.String(),
			ScheduledFor: t.ScheduledFor.Format(time.DateOnly),
			Analyzed:     t.Analyzed,
			Mentioned:    t.Mentioned,
			Percent:      t.Percent,
			ResultIDs:    idStrings(t.ResultIDs),
		})
	}
	return resp
}
