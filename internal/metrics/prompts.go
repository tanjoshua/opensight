package metrics

import (
	"context"
	"fmt"
	"time"

	"opensight/internal/domain"
)

// PromptLatest is an active prompt's latest analyzed result — what drives its
// presence/absence in the Prompts section (design 06). ResultID is the door to
// that single response. Mentioned and MentionOrder come from the mentions table
// (the canonical source); Sentiment from result_analyses.
type PromptLatest struct {
	PromptID     domain.ID
	ResultID     domain.ID
	Mentioned    bool
	MentionOrder *int    // self mention_order, nil when not mentioned
	Sentiment    *string // nil when the business was not mentioned
}

// latestSelfMention keeps, per result, the earliest self mention order — the
// business's rank in the response. A result has at most one self entity, but
// DISTINCT ON is defensive against duplicates.
const latestPromptSQL = `
SELECT DISTINCT ON (pr.prompt_id)
       pr.prompt_id, pr.id, ra.sentiment, sm.mention_order` +
	analyzedJoin + `
JOIN prompts p ON p.id = pr.prompt_id
LEFT JOIN (
  SELECT DISTINCT ON (prompt_result_id) prompt_result_id, mention_order
  FROM mentions WHERE subject = 'self'
  ORDER BY prompt_result_id, mention_order
) sm ON sm.prompt_result_id = pr.id` + analyzedWhere + `
  AND p.status = 'active'
ORDER BY pr.prompt_id, pr.requested_at DESC, pr.id DESC`

// PromptLatestStats returns, for every active prompt with at least one analyzed
// result, that prompt's latest analyzed result and whether the business was
// mentioned in it. Prompts whose latest results are all unanalyzed do not appear
// — presence is gated on the analyzed base like every other metric.
func (m *Metrics) PromptLatestStats(ctx context.Context, tenantID, businessID domain.ID) ([]PromptLatest, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, latestPromptSQL, businessID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("prompt latest stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stats := []PromptLatest{}
	for rows.Next() {
		var p PromptLatest
		if err := rows.Scan(&p.PromptID, &p.ResultID, &p.Sentiment, &p.MentionOrder); err != nil {
			return nil, fmt.Errorf("scan prompt latest: %w", err)
		}
		p.Mentioned = p.MentionOrder != nil
		stats = append(stats, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt latest: %w", err)
	}
	return stats, nil
}

// PromptTrendPoint is one analyzed result of a prompt: whether the business was
// mentioned in that run's response. A prompt's points, oldest week first, are its
// spark-trend across runs (design 06 sparkline). ResultID is the door to that
// single response.
type PromptTrendPoint struct {
	RunID        domain.ID
	ScheduledFor time.Time
	Mentioned    bool
	ResultID     domain.ID
}

const promptTrendsSQL = `
SELECT pr.prompt_id, r.id, r.scheduled_for, pr.id,
       sm.prompt_result_id IS NOT NULL AS mentioned` +
	analyzedJoin + selfMentions + analyzedWhere + `
ORDER BY pr.prompt_id, r.scheduled_for, pr.id`

// PromptTrends returns, per prompt, its analyzed results over time — the
// spark-trend series the Prompts section renders (design 06). Keyed by prompt id;
// each series is oldest week first. Only analyzed results appear, on the same
// gated base as every other metric, so a prompt's trend can never disagree with
// its latest-result summary. Covers active and retired prompts alike; the caller
// selects which prompts it needs.
func (m *Metrics) PromptTrends(ctx context.Context, tenantID, businessID domain.ID) (map[domain.ID][]PromptTrendPoint, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, promptTrendsSQL, businessID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("prompt trends: %w", err)
	}
	defer func() { _ = rows.Close() }()

	trends := map[domain.ID][]PromptTrendPoint{}
	for rows.Next() {
		var promptID domain.ID
		var p PromptTrendPoint
		if err := rows.Scan(&promptID, &p.RunID, &p.ScheduledFor, &p.ResultID, &p.Mentioned); err != nil {
			return nil, fmt.Errorf("scan prompt trend: %w", err)
		}
		trends[promptID] = append(trends[promptID], p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt trends: %w", err)
	}
	return trends, nil
}

// promptChangeDatesSQL is the distinct set of days on which the business's active
// prompt set changed: every prompt's created_at plus every retired prompt's
// retired_at (a replace both retires and creates at the same instant, so it
// collapses to one date). Tenant-scoped via the businesses join.
const promptChangeDatesSQL = `
SELECT DISTINCT d FROM (
  SELECT date_trunc('day', p.created_at) AS d
  FROM prompts p
  JOIN businesses b ON b.id = p.business_id
  WHERE b.id = $1 AND b.tenant_id = $2
  UNION
  SELECT date_trunc('day', p.retired_at) AS d
  FROM prompts p
  JOIN businesses b ON b.id = p.business_id
  WHERE b.id = $1 AND b.tenant_id = $2 AND p.retired_at IS NOT NULL
) x
ORDER BY d`

// PromptChangeDates returns the days on which the business's prompt set changed
// (a prompt created or retired), oldest first. These annotate the visibility
// trend as prompt-set-change markers (design 06) so a prompt change never reads
// as a visibility change. They are prompt-lifecycle dates, not an aggregate over
// results, so they carry no result_ids.
func (m *Metrics) PromptChangeDates(ctx context.Context, tenantID, businessID domain.ID) ([]time.Time, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, promptChangeDatesSQL, businessID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("prompt change dates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	dates := []time.Time{}
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("scan prompt change date: %w", err)
		}
		dates = append(dates, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt change dates: %w", err)
	}
	return dates, nil
}
