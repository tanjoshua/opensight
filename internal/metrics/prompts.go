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

// PromptChange is what happened to the business's prompt set on one day: how many
// prompts were added (created with no predecessor), retired outright (retired with
// no same-day replacement), and replaced (created as a successor). A replace both
// retires the old prompt and inserts its successor at the same instant, so it is
// counted once — as a replace on the successor's creation day — and the old
// prompt's retirement is not double-counted as an outright retire.
type PromptChange struct {
	Date     time.Time
	Added    int
	Retired  int
	Replaced int
}

// promptChangesSQL classifies every prompt-lifecycle event into a per-day
// (added, retired, replaced) tally. Creations split on replaces_prompt_id: NULL is
// an add, non-NULL is a replace. Retirements count only when no same-day insert
// names the retired prompt as its predecessor — that would be the replace already
// counted on the creation side. Tenant-scoped via the businesses join.
const promptChangesSQL = `
SELECT d, sum(added)::int, sum(retired)::int, sum(replaced)::int
FROM (
  SELECT date_trunc('day', p.created_at) AS d,
         CASE WHEN p.replaces_prompt_id IS NULL THEN 1 ELSE 0 END AS added,
         0 AS retired,
         CASE WHEN p.replaces_prompt_id IS NOT NULL THEN 1 ELSE 0 END AS replaced
  FROM prompts p
  JOIN businesses b ON b.id = p.business_id
  WHERE b.id = $1 AND b.tenant_id = $2
  UNION ALL
  SELECT date_trunc('day', p.retired_at) AS d, 0 AS added, 1 AS retired, 0 AS replaced
  FROM prompts p
  JOIN businesses b ON b.id = p.business_id
  WHERE b.id = $1 AND b.tenant_id = $2 AND p.retired_at IS NOT NULL
    AND NOT EXISTS (
      SELECT 1 FROM prompts r
      WHERE r.replaces_prompt_id = p.id
        AND date_trunc('day', r.created_at) = date_trunc('day', p.retired_at)
    )
) e
GROUP BY d
ORDER BY d`

// PromptChanges returns, per day the business's prompt set changed, how many
// prompts were added, retired, and replaced — oldest day first. These annotate the
// visibility trend as prompt-set-change markers (design 06) so a prompt change
// never reads as a visibility change, and the counts let the marker name what
// happened. They are prompt-lifecycle events, not an aggregate over results, so
// they carry no result_ids.
func (m *Metrics) PromptChanges(ctx context.Context, tenantID, businessID domain.ID) ([]PromptChange, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, promptChangesSQL, businessID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("prompt changes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	changes := []PromptChange{}
	for rows.Next() {
		var c PromptChange
		if err := rows.Scan(&c.Date, &c.Added, &c.Retired, &c.Replaced); err != nil {
			return nil, fmt.Errorf("scan prompt change: %w", err)
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt changes: %w", err)
	}
	return changes, nil
}
