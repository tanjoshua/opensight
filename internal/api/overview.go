package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"opensight/internal/domain"
	"opensight/internal/metrics"
	"opensight/internal/store"
)

const (
	// overviewPanelLimit caps the top-keywords and top-cited-domains panels — the
	// Overview panels are compact (design 06); the full lists live in their own
	// sections.
	overviewPanelLimit = 5
	// overviewDiscoveredLimit is the "top-3 discovered by coverage" the Overview
	// competitor panel shows alongside every tracked competitor (MET-2 AC).
	overviewDiscoveredLimit = 3
)

// overviewMetrics is the consumer-side seam over *metrics.Metrics so the handler
// unit-tests against a fake. Every method is tenant-scoped and computes over the
// shared analyzed base (MET-1), so Overview can never disagree with Prompts or
// Competitors on what visibility means.
type overviewMetrics interface {
	VisibilityTrend(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.VisibilityPoint, error)
	KeywordStats(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.KeywordStat, error)
	CitationDomainStats(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.DomainStat, error)
	CompetitorStats(ctx context.Context, tenantID, businessID domain.ID) (metrics.CompetitorStats, error)
	PromptChangeDates(ctx context.Context, tenantID, businessID domain.ID) ([]time.Time, error)
}

type overviewResponse struct {
	Visibility        overviewVisibility          `json:"visibility"`
	PromptChangeDates []string                    `json:"prompt_change_dates"`
	TopKeywords       []keywordResponse           `json:"top_keywords"`
	TopCitedDomains   []domainResponse            `json:"top_cited_domains"`
	TopCompetitors    []competitorSummaryResponse `json:"top_competitors"`
	// DiscoveredTotal is the full count of discovered (untriaged) competitors,
	// before TopCompetitors truncates to the top-3 by coverage — it drives the
	// Overview "N discovered → triage" backlog count, which must not undercount.
	DiscoveredTotal int          `json:"discovered_total"`
	LatestRun       *runResponse `json:"latest_run"`
}

// overviewVisibility is the headline stat plus the weekly trend. Current is the
// latest analyzed run's visibility %; Delta is current minus the previous run's
// (both nil until there is enough analyzed history to fill them). Trend is the
// full series each point of which carries its result_ids — every week is a door.
type overviewVisibility struct {
	Current *float64                  `json:"current"`
	Delta   *float64                  `json:"delta"`
	Trend   []visibilityPointResponse `json:"trend"`
}

type visibilityPointResponse struct {
	RunID        string   `json:"run_id"`
	ScheduledFor string   `json:"scheduled_for"`
	Analyzed     int      `json:"analyzed"`
	Mentioned    int      `json:"mentioned"`
	Percent      float64  `json:"percent"`
	ResultIDs    []string `json:"result_ids"`
}

type keywordResponse struct {
	Keyword   string   `json:"keyword"`
	ResultIDs []string `json:"result_ids"`
}

type domainResponse struct {
	Domain    string   `json:"domain"`
	ResultIDs []string `json:"result_ids"`
}

type competitorSummaryResponse struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Mentioned      int      `json:"mentioned"`
	TotalMentions  int      `json:"total_mentions"`
	MentionPercent float64  `json:"mention_percent"`
	AvgOrder       float64  `json:"avg_order"`
	VsSelf         float64  `json:"vs_self"`
	ResultIDs      []string `json:"result_ids"`
}

// handleGetOverview assembles the single Overview payload from the shared metrics
// package (MET-2). ListRuns doubles as the business→tenant ownership gate: it
// returns ErrNotFound for a missing or cross-tenant business, so the metrics
// calls below (which return empty rather than erroring for an unowned business)
// only run once ownership is proven.
func (s *Server) handleGetOverview(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil || s.runs == nil {
		s.writeInternalError(w, "overview: store missing", errors.New("metrics and run stores are required"))
		return
	}

	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "overview: missing session context", errors.New("missing session context"))
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

	trend, err := s.metrics.VisibilityTrend(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	keywords, err := s.metrics.KeywordStats(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	domains, err := s.metrics.CitationDomainStats(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	competitors, err := s.metrics.CompetitorStats(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	changeDates, err := s.metrics.PromptChangeDates(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	topCompetitors, discoveredTotal := topCompetitorsToResponse(competitors)
	resp := overviewResponse{
		Visibility:        visibilityToResponse(trend),
		PromptChangeDates: datesToResponse(changeDates),
		TopKeywords:       keywordsToResponse(keywords),
		TopCitedDomains:   domainsToResponse(domains),
		TopCompetitors:    topCompetitors,
		DiscoveredTotal:   discoveredTotal,
		LatestRun:         latestRunToResponse(runs),
	}
	writeJSON(w, http.StatusOK, resp)
}

// visibilityToResponse shapes the trend and derives the headline current/delta.
// VisibilityTrend is oldest-first, so the last point is the current run and the
// second-to-last is its predecessor (design 06: delta is the caller's subtraction).
func visibilityToResponse(trend []metrics.VisibilityPoint) overviewVisibility {
	v := overviewVisibility{Trend: make([]visibilityPointResponse, 0, len(trend))}
	for _, p := range trend {
		v.Trend = append(v.Trend, visibilityPointResponse{
			RunID:        p.RunID.String(),
			ScheduledFor: p.ScheduledFor.Format(time.DateOnly),
			Analyzed:     p.Analyzed,
			Mentioned:    p.Mentioned,
			Percent:      p.Percent,
			ResultIDs:    idStrings(p.ResultIDs),
		})
	}
	if n := len(trend); n > 0 {
		current := trend[n-1].Percent
		v.Current = &current
		if n > 1 {
			delta := current - trend[n-2].Percent
			v.Delta = &delta
		}
	}
	return v
}

func keywordsToResponse(stats []metrics.KeywordStat) []keywordResponse {
	out := make([]keywordResponse, 0, min(len(stats), overviewPanelLimit))
	for _, s := range stats[:min(len(stats), overviewPanelLimit)] {
		out = append(out, keywordResponse{Keyword: s.Keyword, ResultIDs: idStrings(s.ResultIDs)})
	}
	return out
}

func domainsToResponse(stats []metrics.DomainStat) []domainResponse {
	out := make([]domainResponse, 0, min(len(stats), overviewPanelLimit))
	for _, s := range stats[:min(len(stats), overviewPanelLimit)] {
		out = append(out, domainResponse{Domain: s.Domain, ResultIDs: idStrings(s.ResultIDs)})
	}
	return out
}

// topCompetitorsToResponse keeps every tracked competitor plus the top-3
// discovered by coverage (MET-2 AC), dropping dismissed ones (design 05: a
// display filter, the data is retained). CompetitorStats is coverage-desc, so
// the discovered cap keeps the three with the widest coverage. It also returns
// the full discovered count (before the cap) so the "N discovered → triage"
// backlog reflects the real number, not the truncated panel.
func topCompetitorsToResponse(stats metrics.CompetitorStats) ([]competitorSummaryResponse, int) {
	out := []competitorSummaryResponse{}
	shown, total := 0, 0
	for _, c := range stats.Competitors {
		switch c.Status {
		case "tracked":
		case "discovered":
			total++
			if shown >= overviewDiscoveredLimit {
				continue
			}
			shown++
		default: // dismissed
			continue
		}
		out = append(out, competitorSummaryResponse{
			ID:             c.CompetitorID.String(),
			Name:           c.Name,
			Status:         c.Status,
			Mentioned:      c.Mentioned,
			TotalMentions:  c.TotalMentions,
			MentionPercent: c.MentionPercent,
			AvgOrder:       c.AvgOrder,
			VsSelf:         c.VsSelf,
			ResultIDs:      idStrings(c.ResultIDs),
		})
	}
	return out, total
}

func datesToResponse(dates []time.Time) []string {
	out := make([]string, 0, len(dates))
	for _, d := range dates {
		out = append(out, d.UTC().Format(time.DateOnly))
	}
	return out
}

// latestRunToResponse returns the most recent run (ListRuns is scheduled_for
// DESC) so the Overview can show run status and drive the degraded/first-run
// states, or nil before the first run exists.
func latestRunToResponse(runs []store.Run) *runResponse {
	if len(runs) == 0 {
		return nil
	}
	r := runToResponse(runs[0])
	return &r
}

func idStrings(ids []domain.ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
