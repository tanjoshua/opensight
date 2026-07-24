package metrics

import (
	"context"
	"fmt"
	"time"

	"opensight/internal/domain"
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

const competitorOverallSQL = `
SELECT co.id, co.name, co.status,
       to_jsonb(co.aliases), to_jsonb(co.suggested_aliases),
       count(DISTINCT am.result_id) AS mentioned,
       count(am.mention_id) AS total_mentions,
       coalesce(avg(am.mention_order), 0)::float8 AS avg_order,
       coalesce(
         to_jsonb(array_agg(DISTINCT am.result_id) FILTER (WHERE am.result_id IS NOT NULL)),
         '[]'::jsonb
       ) AS result_ids
FROM competitors co
JOIN businesses owner ON owner.id = co.business_id
LEFT JOIN (
  SELECT m.competitor_id, m.id AS mention_id, m.mention_order, pr.id AS result_id` +
	analyzedJoin + `
  JOIN mentions m ON m.prompt_result_id = pr.id AND m.subject = 'competitor'` + analyzedWhere + `
) am ON am.competitor_id = co.id
WHERE co.business_id = $1 AND owner.tenant_id = $2
GROUP BY co.id, co.name, co.status, co.aliases, co.suggested_aliases
ORDER BY count(DISTINCT am.result_id) DESC, co.name`

const competitorTrendSQL = `
SELECT m.competitor_id, r.id, r.scheduled_for,
       count(DISTINCT pr.id) AS mentioned,
       to_jsonb(array_agg(DISTINCT pr.id)) AS result_ids` +
	analyzedJoin + `
JOIN mentions m ON m.prompt_result_id = pr.id AND m.subject = 'competitor'` + analyzedWhere + `
GROUP BY m.competitor_id, r.id, r.scheduled_for
ORDER BY r.scheduled_for`

const competitorPerPromptSQL = `
SELECT m.competitor_id, pr.prompt_id, p.text,
       to_jsonb(array_agg(DISTINCT pr.id)) AS result_ids` +
	analyzedJoin + `
JOIN mentions m ON m.prompt_result_id = pr.id AND m.subject = 'competitor'
JOIN prompts p ON p.id = pr.prompt_id` + analyzedWhere + `
GROUP BY m.competitor_id, pr.prompt_id, p.text
ORDER BY count(DISTINCT pr.id) DESC, p.text`

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
	rows, err := m.db.QueryContext(ctx, competitorOverallSQL, businessID, tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("competitor overall stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stats := map[domain.ID]*CompetitorStat{}
	order := []domain.ID{}
	for rows.Next() {
		var s CompetitorStat
		var ids resultIDs
		var aliases, suggestedAliases stringValues
		if err := rows.Scan(
			&s.CompetitorID,
			&s.Name,
			&s.Status,
			&aliases,
			&suggestedAliases,
			&s.Mentioned,
			&s.TotalMentions,
			&s.AvgOrder,
			&ids,
		); err != nil {
			return nil, nil, fmt.Errorf("scan competitor stat: %w", err)
		}
		s.Aliases = aliases
		s.SuggestedAliases = suggestedAliases
		s.ResultIDs = ids
		s.MentionPercent = percent(s.Mentioned, totalAnalyzed)
		s.VsSelf = s.MentionPercent - selfPercent
		s.PerPrompt = []PromptAppearance{}
		s.Trend = []CompetitorTrendPoint{}
		stats[s.CompetitorID] = &s
		order = append(order, s.CompetitorID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate competitor stats: %w", err)
	}
	return stats, order, nil
}

// competitorTrends attaches each competitor's weekly mention %, including
// analyzed runs where that competitor was absent.
func (m *Metrics) competitorTrends(ctx context.Context, tenantID, businessID domain.ID, stats map[domain.ID]*CompetitorStat, visibility []VisibilityPoint) error {
	rows, err := m.db.QueryContext(ctx, competitorTrendSQL, businessID, tenantID)
	if err != nil {
		return fmt.Errorf("competitor trend: %w", err)
	}
	defer func() { _ = rows.Close() }()

	mentionedByRun := map[domain.ID]map[domain.ID]CompetitorTrendPoint{}
	for rows.Next() {
		var competitorID domain.ID
		var point CompetitorTrendPoint
		var ids resultIDs
		if err := rows.Scan(&competitorID, &point.RunID, &point.ScheduledFor, &point.Mentioned, &ids); err != nil {
			return fmt.Errorf("scan competitor trend: %w", err)
		}
		if _, ok := stats[competitorID]; !ok {
			continue
		}
		if _, ok := mentionedByRun[competitorID]; !ok {
			mentionedByRun[competitorID] = map[domain.ID]CompetitorTrendPoint{}
		}
		point.ResultIDs = ids
		mentionedByRun[competitorID][point.RunID] = point
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate competitor trend: %w", err)
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
	rows, err := m.db.QueryContext(ctx, competitorPerPromptSQL, businessID, tenantID)
	if err != nil {
		return fmt.Errorf("competitor per-prompt: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var competitorID domain.ID
		var appearance PromptAppearance
		var ids resultIDs
		if err := rows.Scan(&competitorID, &appearance.PromptID, &appearance.Text, &ids); err != nil {
			return fmt.Errorf("scan competitor per-prompt: %w", err)
		}
		s, ok := stats[competitorID]
		if !ok {
			continue
		}
		appearance.ResultIDs = ids
		s.PerPrompt = append(s.PerPrompt, appearance)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate competitor per-prompt: %w", err)
	}
	return nil
}
