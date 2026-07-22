package metrics

import (
	"context"
	"fmt"
	"time"

	"opensight/internal/domain"
)

// VisibilityPoint is one run's visibility: what fraction of the run's analyzed
// results mentioned the business. Analyzed is the denominator (analyzed results
// in the run), Mentioned the numerator (those with a self mention). ResultIDs
// are the analyzed results behind the point — the door: clicking the week opens
// Responses filtered to this run (design 06).
type VisibilityPoint struct {
	RunID        domain.ID
	ScheduledFor time.Time
	Analyzed     int
	Mentioned    int
	Percent      float64
	ResultIDs    []domain.ID
}

const visibilityTrendSQL = `
SELECT r.id, r.scheduled_for,
       count(*) AS analyzed,
       count(*) FILTER (WHERE sm.prompt_result_id IS NOT NULL) AS mentioned,
       to_jsonb(array_agg(pr.id ORDER BY pr.id)) AS result_ids` +
	analyzedJoin + selfMentions + analyzedWhere + `
GROUP BY r.id, r.scheduled_for
ORDER BY r.scheduled_for`

// VisibilityTrend returns the business's visibility per analyzed run, oldest
// week first (design 02: "self-mentions ÷ analyzed results", grouped by
// scheduled_for). The latest point is the current visibility %; the delta versus
// the previous run is the caller's subtraction. Runs without analysis_completed_at
// and results without a result_analyses row are excluded from both sides.
func (m *Metrics) VisibilityTrend(ctx context.Context, tenantID, businessID domain.ID) ([]VisibilityPoint, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, visibilityTrendSQL, businessID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("visibility trend: %w", err)
	}
	defer func() { _ = rows.Close() }()

	points := []VisibilityPoint{}
	for rows.Next() {
		var p VisibilityPoint
		var ids resultIDs
		if err := rows.Scan(&p.RunID, &p.ScheduledFor, &p.Analyzed, &p.Mentioned, &ids); err != nil {
			return nil, fmt.Errorf("scan visibility point: %w", err)
		}
		p.ResultIDs = ids
		p.Percent = percent(p.Mentioned, p.Analyzed)
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate visibility points: %w", err)
	}
	return points, nil
}
