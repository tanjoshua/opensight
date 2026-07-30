package metrics

import (
	"context"
	"fmt"
	"time"

	"opensight/internal/domain"
	db "opensight/internal/store/sqlc"
)

// CompetitorStats is the whole competitor comparison for a business. TotalAnalyzed
// is the shared denominator (analyzed results across all runs); SelfMentioned and
// SelfPercent are the business's own coverage over that same base, so each
// competitor's VsSelf is directly comparable (design 02: competitor comparison
// vs subject='self').
type CompetitorStats struct {
	TotalAnalyzed int
	SelfMentioned int
	SelfPercent   float64
	ResultIDs     []domain.ID
	Competitors   []CompetitorStat
}

// CompetitorStat is one competitor's stats over the analyzed base (design 06/
// PRD §6: mention %, totals, avg order, per-prompt appearances, trend, vs-self).
// Mentioned counts distinct analyzed results mentioning the competitor;
// TotalMentions counts mention rows. ResultIDs are those results — the door.
type CompetitorStat struct {
	CompetitorID     domain.ID
	Name             string
	Status           string // discovered|tracked|dismissed (display filter is the caller's)
	Aliases          []string
	SuggestedAliases []string
	Mentioned        int
	TotalMentions    int
	MentionPercent   float64
	AvgOrder         float64
	VsSelf           float64 // MentionPercent - SelfPercent
	ResultIDs        []domain.ID
	PerPrompt        []PromptAppearance
	Trend            []CompetitorTrendPoint
}

// PromptAppearance is the analyzed results of one prompt in which a competitor
// appeared — "in N of the responses to this prompt". Coverage is len(ResultIDs).
type PromptAppearance struct {
	PromptID  domain.ID
	Text      string
	ResultIDs []domain.ID
}

// CompetitorTrendPoint is a competitor's mention % for one analyzed run. Analyzed
// is the run's denominator (shared with the business's own visibility that week).
type CompetitorTrendPoint struct {
	RunID        domain.ID
	ScheduledFor time.Time
	Analyzed     int
	Mentioned    int
	Percent      float64
	ResultIDs    []domain.ID
}

// CompetitorStats computes every competitor's comparison stats over the analyzed
// base. It reuses VisibilityTrend for the per-run and overall denominators so a
// competitor's mention % and the business's own visibility % are always measured
// against the identical result set. Dismissed competitors are included (design 02:
// dismissal is a display filter); the caller filters by status.
func (m *Metrics) CompetitorStats(ctx context.Context, tenantID, businessID domain.ID) (CompetitorStats, error) {
	if err := m.ready(); err != nil {
		return CompetitorStats{}, err
	}

	// The denominators come from the same visibility computation the Overview
	// uses, so competitor % and self % can never diverge on their base.
	trend, err := m.VisibilityTrend(ctx, tenantID, businessID)
	if err != nil {
		return CompetitorStats{}, err
	}
	result := CompetitorStats{Competitors: []CompetitorStat{}}
	for _, p := range trend {
		result.TotalAnalyzed += p.Analyzed
		result.SelfMentioned += p.Mentioned
		result.ResultIDs = append(result.ResultIDs, p.ResultIDs...)
	}
	result.SelfPercent = percent(result.SelfMentioned, result.TotalAnalyzed)

	stats, order, err := m.competitorOverall(ctx, tenantID, businessID, result.TotalAnalyzed, result.SelfPercent)
	if err != nil {
		return CompetitorStats{}, err
	}
	if err := m.competitorTrends(ctx, tenantID, businessID, stats, trend); err != nil {
		return CompetitorStats{}, err
	}
	if err := m.competitorPerPrompt(ctx, tenantID, businessID, stats); err != nil {
		return CompetitorStats{}, err
	}

	for _, id := range order {
		result.Competitors = append(result.Competitors, *stats[id])
	}
	return result, nil
}

// competitorOverall loads each competitor's totals and returns them keyed by id
// plus the descending-coverage order the SQL produced.
func (m *Metrics) competitorOverall(ctx context.Context, tenantID, businessID domain.ID, totalAnalyzed int, selfPercent float64) (map[domain.ID]*CompetitorStat, []domain.ID, error) {
	rows, err := m.q.CompetitorOverall(ctx, db.CompetitorOverallParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return nil, nil, fmt.Errorf("competitor overall stats: %w", err)
	}

	stats := map[domain.ID]*CompetitorStat{}
	order := make([]domain.ID, 0, len(rows))
	for _, r := range rows {
		s := &CompetitorStat{
			CompetitorID:     r.ID,
			Name:             r.Name,
			Status:           r.Status,
			Aliases:          r.Aliases,
			SuggestedAliases: r.SuggestedAliases,
			Mentioned:        int(r.Mentioned),
			TotalMentions:    int(r.TotalMentions),
			AvgOrder:         r.AvgOrder,
			ResultIDs:        r.ResultIds,
			PerPrompt:        []PromptAppearance{},
			Trend:            []CompetitorTrendPoint{},
		}
		s.MentionPercent = percent(s.Mentioned, totalAnalyzed)
		s.VsSelf = s.MentionPercent - selfPercent
		stats[s.CompetitorID] = s
		order = append(order, s.CompetitorID)
	}
	return stats, order, nil
}

// competitorTrends attaches each competitor's weekly mention %, including
// analyzed runs where that competitor was absent.
func (m *Metrics) competitorTrends(ctx context.Context, tenantID, businessID domain.ID, stats map[domain.ID]*CompetitorStat, visibility []VisibilityPoint) error {
	rows, err := m.q.CompetitorTrend(ctx, db.CompetitorTrendParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("competitor trend: %w", err)
	}

	mentionedByRun := map[domain.ID]map[domain.ID]CompetitorTrendPoint{}
	for _, r := range rows {
		// mentions.competitor_id is required when subject='competitor' (schema
		// CHECK), which this query filters on; nil is defensive only, sqlc
		// can't see the CHECK and types the LEFT JOIN column as nullable.
		if r.CompetitorID == nil {
			continue
		}
		competitorID := *r.CompetitorID
		if _, ok := stats[competitorID]; !ok {
			continue
		}
		if _, ok := mentionedByRun[competitorID]; !ok {
			mentionedByRun[competitorID] = map[domain.ID]CompetitorTrendPoint{}
		}
		mentionedByRun[competitorID][r.RunID] = CompetitorTrendPoint{
			RunID:        r.RunID,
			ScheduledFor: r.ScheduledFor,
			Mentioned:    int(r.Mentioned),
			ResultIDs:    r.ResultIds,
		}
	}
	for competitorID, s := range stats {
		for _, base := range visibility {
			point, ok := mentionedByRun[competitorID][base.RunID]
			if !ok {
				point = CompetitorTrendPoint{
					RunID:        base.RunID,
					ScheduledFor: base.ScheduledFor,
					ResultIDs:    []domain.ID{},
				}
			}
			point.Analyzed = base.Analyzed
			point.Percent = percent(point.Mentioned, point.Analyzed)
			s.Trend = append(s.Trend, point)
		}
	}
	return nil
}

// competitorPerPrompt attaches each competitor's per-prompt appearances.
func (m *Metrics) competitorPerPrompt(ctx context.Context, tenantID, businessID domain.ID, stats map[domain.ID]*CompetitorStat) error {
	rows, err := m.q.CompetitorPerPrompt(ctx, db.CompetitorPerPromptParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("competitor per-prompt: %w", err)
	}

	for _, r := range rows {
		if r.CompetitorID == nil {
			continue
		}
		s, ok := stats[*r.CompetitorID]
		if !ok {
			continue
		}
		s.PerPrompt = append(s.PerPrompt, PromptAppearance{
			PromptID:  r.PromptID,
			Text:      r.PromptText,
			ResultIDs: r.ResultIds,
		})
	}
	return nil
}
