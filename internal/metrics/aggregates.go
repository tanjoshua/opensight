package metrics

import (
	"context"
	"fmt"

	"opensight/internal/domain"
)

// KeywordStat is one keyword and the analyzed results it was extracted from
// (design 02: sentiment/keywords from result_analyses). Frequency is
// len(ResultIDs) — the number of responses that characterized the business with
// this keyword.
type KeywordStat struct {
	Keyword   string
	ResultIDs []domain.ID
}

// SentimentStat is one sentiment value and the analyzed results with it. NULL
// sentiment (business not mentioned) is excluded — sentiment describes how a
// response characterizes the business, so it is only meaningful where the
// business appears.
type SentimentStat struct {
	Sentiment string
	ResultIDs []domain.ID
}

// DomainStat is one cited domain and the analyzed results that cited it (design
// 02: citation frequency by domain). Frequency is len(ResultIDs).
type DomainStat struct {
	Domain    string
	ResultIDs []domain.ID
}

const keywordStatsSQL = `
SELECT kw, to_jsonb(array_agg(DISTINCT pr.id)) AS result_ids` +
	analyzedJoin + `
CROSS JOIN LATERAL unnest(ra.keywords) AS kw` + analyzedWhere + `
GROUP BY kw
ORDER BY count(DISTINCT pr.id) DESC, kw`

const sentimentStatsSQL = `
SELECT ra.sentiment, to_jsonb(array_agg(pr.id ORDER BY pr.id)) AS result_ids` +
	analyzedJoin + analyzedWhere + `
  AND ra.sentiment IS NOT NULL
GROUP BY ra.sentiment
ORDER BY count(*) DESC, ra.sentiment`

const citationDomainStatsSQL = `
SELECT c.domain, to_jsonb(array_agg(DISTINCT pr.id)) AS result_ids` +
	analyzedJoin + `
JOIN citations c ON c.prompt_result_id = pr.id` + analyzedWhere + `
GROUP BY c.domain
ORDER BY count(DISTINCT pr.id) DESC, c.domain`

// KeywordStats returns the business's keywords by descending frequency across
// all analyzed results.
func (m *Metrics) KeywordStats(ctx context.Context, tenantID, businessID domain.ID) ([]KeywordStat, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx, keywordStatsSQL, businessID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("keyword stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stats := []KeywordStat{}
	for rows.Next() {
		var s KeywordStat
		var ids resultIDs
		if err := rows.Scan(&s.Keyword, &ids); err != nil {
			return nil, fmt.Errorf("scan keyword stat: %w", err)
		}
		s.ResultIDs = ids
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate keyword stats: %w", err)
	}
	return stats, nil
}

// SentimentStats returns the distribution of business sentiment across all
// analyzed results, most common first.
func (m *Metrics) SentimentStats(ctx context.Context, tenantID, businessID domain.ID) ([]SentimentStat, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx, sentimentStatsSQL, businessID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("sentiment stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stats := []SentimentStat{}
	for rows.Next() {
		var s SentimentStat
		var ids resultIDs
		if err := rows.Scan(&s.Sentiment, &ids); err != nil {
			return nil, fmt.Errorf("scan sentiment stat: %w", err)
		}
		s.ResultIDs = ids
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sentiment stats: %w", err)
	}
	return stats, nil
}

// CitationDomainStats returns cited domains by descending frequency across all
// analyzed results.
func (m *Metrics) CitationDomainStats(ctx context.Context, tenantID, businessID domain.ID) ([]DomainStat, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx, citationDomainStatsSQL, businessID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("citation domain stats: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stats := []DomainStat{}
	for rows.Next() {
		var s DomainStat
		var ids resultIDs
		if err := rows.Scan(&s.Domain, &ids); err != nil {
			return nil, fmt.Errorf("scan domain stat: %w", err)
		}
		s.ResultIDs = ids
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domain stats: %w", err)
	}
	return stats, nil
}
