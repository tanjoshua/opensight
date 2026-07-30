package metrics

import (
	"context"
	"fmt"
	"time"

	"opensight/internal/domain"
	db "opensight/internal/store/sqlc"
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

// PromptLatestStats returns, for every active prompt with at least one analyzed
// result, that prompt's latest analyzed result and whether the business was
// mentioned in it. Prompts whose latest results are all unanalyzed do not appear
// — presence is gated on the analyzed base like every other metric.
func (m *Metrics) PromptLatestStats(ctx context.Context, tenantID, businessID domain.ID) ([]PromptLatest, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}

	rows, err := m.q.PromptLatestStats(ctx, db.PromptLatestStatsParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("prompt latest stats: %w", err)
	}

	stats := make([]PromptLatest, 0, len(rows))
	for _, r := range rows {
		p := PromptLatest{
			PromptID:  r.PromptID,
			ResultID:  r.ResultID,
			Mentioned: r.Mentioned,
			Sentiment: r.Sentiment,
		}
		if r.Mentioned {
			order := int(r.MentionOrder)
			p.MentionOrder = &order
		}
		stats = append(stats, p)
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

	rows, err := m.q.PromptTrends(ctx, db.PromptTrendsParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("prompt trends: %w", err)
	}

	trends := map[domain.ID][]PromptTrendPoint{}
	for _, r := range rows {
		trends[r.PromptID] = append(trends[r.PromptID], PromptTrendPoint{
			RunID:        r.RunID,
			ScheduledFor: r.ScheduledFor,
			Mentioned:    r.Mentioned,
			ResultID:     r.ResultID,
		})
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

	rows, err := m.q.PromptChanges(ctx, db.PromptChangesParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("prompt changes: %w", err)
	}

	changes := make([]PromptChange, 0, len(rows))
	for _, r := range rows {
		changes = append(changes, PromptChange{
			Date:     r.D,
			Added:    int(r.Added),
			Retired:  int(r.Retired),
			Replaced: int(r.Replaced),
		})
	}
	return changes, nil
}
