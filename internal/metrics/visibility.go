package metrics

import (
	"context"
	"fmt"
	"time"

	"opensight/internal/domain"
	db "opensight/internal/store/sqlc"
)

// VisibilityPoint is one run's visibility: what fraction of the run's analyzed
// results mentioned the business. Analyzed is the denominator (analyzed
// results in the run), Mentioned the numerator (those with a self mention).
// ResultIDs are the analyzed results behind the point — the door: clicking
// the week opens Responses filtered to this run (design 06).
type VisibilityPoint struct {
	RunID        domain.ID
	ScheduledFor time.Time
	Analyzed     int
	Mentioned    int
	Percent      float64
	ResultIDs    []domain.ID
}

// VisibilityTrend returns the business's visibility per analyzed run, oldest
// week first (design 02: "self-mentions ÷ analyzed results", grouped by
// scheduled_for). The latest point is the current visibility %; the delta
// versus the previous run is the caller's subtraction. Runs without
// analysis_completed_at and results without a result_analyses row are
// excluded from both sides.
func (m *Metrics) VisibilityTrend(ctx context.Context, tenantID, businessID domain.ID) ([]VisibilityPoint, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}

	rows, err := m.q.VisibilityTrend(ctx, db.VisibilityTrendParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("visibility trend: %w", err)
	}

	points := make([]VisibilityPoint, 0, len(rows))
	for _, r := range rows {
		points = append(points, VisibilityPoint{
			RunID:        r.RunID,
			ScheduledFor: r.ScheduledFor,
			Analyzed:     int(r.Analyzed),
			Mentioned:    int(r.Mentioned),
			Percent:      percent(int(r.Mentioned), int(r.Analyzed)),
			ResultIDs:    r.ResultIds,
		})
	}
	return points, nil
}
