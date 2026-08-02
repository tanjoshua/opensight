package metrics

import (
	"context"
	"fmt"
	"sort"

	"opensight/internal/domain"
	db "opensight/internal/store/sqlc"
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

// CitationSubjectStat is one subject bucket inside a citation source aggregate.
// Frequency is len(ResultIDs): distinct analyzed responses, matching Overview's
// cited-domain frequency rather than duplicate annotations inside one answer.
type CitationSubjectStat struct {
	Frequency int
	ResultIDs []domain.ID
}

// CitationSubjectBreakdown is the per-source subject split requested by PRD §6:
// which analyzed responses had citations judged to support the business,
// competitors, other sources, or an honest unknown.
type CitationSubjectBreakdown struct {
	Business   CitationSubjectStat
	Competitor CitationSubjectStat
	Other      CitationSubjectStat
	Unknown    CitationSubjectStat
}

// CitationSource is the full citation-sources drill-down for one cited
// domain. Frequency is the number of distinct analyzed responses citing this
// domain; ResultIDs is the door behind that number.
type CitationSource struct {
	Domain    string
	Frequency int
	Subjects  CitationSubjectBreakdown
	ResultIDs []domain.ID
	Pages     []CitationPage
	Prompts   []CitationPrompt
}

// CitationPage is one normalized cited URL under a domain.
type CitationPage struct {
	URL       string
	Title     *string
	Frequency int
	Subjects  CitationSubjectBreakdown
	ResultIDs []domain.ID
}

// CitationPrompt is one prompt associated with a cited domain.
type CitationPrompt struct {
	PromptID  domain.ID
	Text      string
	Frequency int
	ResultIDs []domain.ID
}

// KeywordStats returns the business's keywords by descending frequency across
// all analyzed results.
func (m *Metrics) KeywordStats(ctx context.Context, tenantID, businessID domain.ID) ([]KeywordStat, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	rows, err := m.q.KeywordStats(ctx, db.KeywordStatsParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("keyword stats: %w", err)
	}
	stats := make([]KeywordStat, 0, len(rows))
	for _, r := range rows {
		stats = append(stats, KeywordStat{Keyword: r.Keyword, ResultIDs: r.ResultIds})
	}
	return stats, nil
}

// SentimentStats returns the distribution of business sentiment across all
// analyzed results, most common first.
func (m *Metrics) SentimentStats(ctx context.Context, tenantID, businessID domain.ID) ([]SentimentStat, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	rows, err := m.q.SentimentStats(ctx, db.SentimentStatsParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("sentiment stats: %w", err)
	}
	stats := make([]SentimentStat, 0, len(rows))
	for _, r := range rows {
		stats = append(stats, SentimentStat{Sentiment: derefString(r.Sentiment), ResultIDs: r.ResultIds})
	}
	return stats, nil
}

// CitationDomainStats returns cited domains by descending frequency across all
// analyzed results.
func (m *Metrics) CitationDomainStats(ctx context.Context, tenantID, businessID domain.ID) ([]DomainStat, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	rows, err := m.q.CitationDomainStats(ctx, db.CitationDomainStatsParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("citation domain stats: %w", err)
	}
	stats := make([]DomainStat, 0, len(rows))
	for _, r := range rows {
		stats = append(stats, DomainStat{Domain: r.Domain, ResultIDs: r.ResultIds})
	}
	return stats, nil
}

// CitationSources returns the full citation-sources drill-down across all
// analyzed results: domains, cited pages, associated prompts, subject splits,
// and result_id doors for every aggregate.
func (m *Metrics) CitationSources(ctx context.Context, tenantID, businessID domain.ID) ([]CitationSource, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	rows, err := m.q.CitationSources(ctx, db.CitationSourcesParams{BusinessID: businessID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("citation sources: %w", err)
	}

	byDomain := map[string]*citationSourceAgg{}
	for _, r := range rows {
		source := byDomain[r.Domain]
		if source == nil {
			source = &citationSourceAgg{
				CitationSource: CitationSource{Domain: r.Domain},
				resultSeen:     map[domain.ID]bool{},
				pages:          map[string]*citationPageAgg{},
				prompts:        map[domain.ID]*citationPromptAgg{},
			}
			byDomain[r.Domain] = source
		}
		if appendUniqueID(&source.ResultIDs, source.resultSeen, r.ResultID) {
			source.Frequency = len(source.ResultIDs)
		}
		source.Subjects.add(r.Subject, r.ResultID)

		page := source.pages[r.Url]
		if page == nil {
			page = &citationPageAgg{
				CitationPage: CitationPage{URL: r.Url},
				resultSeen:   map[domain.ID]bool{},
			}
			source.pages[r.Url] = page
		}
		if page.Title == nil && r.Title != nil {
			page.Title = r.Title
		}
		if appendUniqueID(&page.ResultIDs, page.resultSeen, r.ResultID) {
			page.Frequency = len(page.ResultIDs)
		}
		page.Subjects.add(r.Subject, r.ResultID)

		prompt := source.prompts[r.PromptID]
		if prompt == nil {
			prompt = &citationPromptAgg{
				CitationPrompt: CitationPrompt{PromptID: r.PromptID, Text: r.PromptText},
				resultSeen:     map[domain.ID]bool{},
			}
			source.prompts[r.PromptID] = prompt
		}
		if appendUniqueID(&prompt.ResultIDs, prompt.resultSeen, r.ResultID) {
			prompt.Frequency = len(prompt.ResultIDs)
		}
	}

	sources := make([]CitationSource, 0, len(byDomain))
	for _, source := range byDomain {
		for _, page := range source.pages {
			source.Pages = append(source.Pages, page.CitationPage)
		}
		sort.Slice(source.Pages, func(i, j int) bool {
			if source.Pages[i].Frequency != source.Pages[j].Frequency {
				return source.Pages[i].Frequency > source.Pages[j].Frequency
			}
			return source.Pages[i].URL < source.Pages[j].URL
		})
		for _, prompt := range source.prompts {
			source.Prompts = append(source.Prompts, prompt.CitationPrompt)
		}
		sort.Slice(source.Prompts, func(i, j int) bool {
			if source.Prompts[i].Frequency != source.Prompts[j].Frequency {
				return source.Prompts[i].Frequency > source.Prompts[j].Frequency
			}
			return source.Prompts[i].Text < source.Prompts[j].Text
		})
		sources = append(sources, source.CitationSource)
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Frequency != sources[j].Frequency {
			return sources[i].Frequency > sources[j].Frequency
		}
		return sources[i].Domain < sources[j].Domain
	})
	return sources, nil
}

type citationSourceAgg struct {
	CitationSource
	resultSeen map[domain.ID]bool
	pages      map[string]*citationPageAgg
	prompts    map[domain.ID]*citationPromptAgg
}

type citationPageAgg struct {
	CitationPage
	resultSeen map[domain.ID]bool
}

type citationPromptAgg struct {
	CitationPrompt
	resultSeen map[domain.ID]bool
}

func (b *CitationSubjectBreakdown) add(subject string, resultID domain.ID) {
	switch subject {
	case "business":
		b.Business.add(resultID)
	case "competitor":
		b.Competitor.add(resultID)
	case "other":
		b.Other.add(resultID)
	default:
		b.Unknown.add(resultID)
	}
}

func (s *CitationSubjectStat) add(resultID domain.ID) {
	if appendUniqueIDByScan(&s.ResultIDs, resultID) {
		s.Frequency = len(s.ResultIDs)
	}
}

func appendUniqueID(ids *[]domain.ID, seen map[domain.ID]bool, id domain.ID) bool {
	if seen[id] {
		return false
	}
	seen[id] = true
	*ids = append(*ids, id)
	return true
}

func appendUniqueIDByScan(ids *[]domain.ID, id domain.ID) bool {
	for _, existing := range *ids {
		if existing == id {
			return false
		}
	}
	*ids = append(*ids, id)
	return true
}
