package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// The methods below perform the wipe half of the derived-analysis tables'
// wipe-and-rebuild contract (design 02, 05). Analysis data is derived from
// prompt_results and rebuildable, so the pipeline overwrites its own outputs
// idempotently: AnalyzeResult re-runs delete a result's analysis before
// rewriting it; ReconcileEntities deletes-and-rewrites a run's mentions in one
// transaction. The raw prompt_results and monitoring_runs rows are never touched
// by these deletes.
//
// The insert/upsert surface (result_analyses, citations, competitors, mentions
// writes) lives in the workflow activities, not here.

// CitationWrite is one citation row for SaveResultAnalysis, already normalized
// (URL cleaned, domain extracted) and ordered by first appearance.
type CitationWrite struct {
	URL       string
	Domain    string
	Title     *string
	CiteOrder int
	Subject   string // business|competitor|other|unknown
}

// SaveResultAnalysisParams is the write payload for one analyzed result.
type SaveResultAnalysisParams struct {
	PromptResultID    domain.ID
	Sentiment         *string // nil == business not mentioned
	Keywords          []string
	Excerpts          []string
	AnalysisModel     string
	ExtractionVersion int
	Citations         []CitationWrite
}

// SaveResultAnalysis is AnalyzeResult's write path (design 05: "AnalyzeResult
// upserts by prompt_result_id"). In one transaction it verifies the result
// belongs to tenantID, then unconditionally overwrites: ON CONFLICT DO UPDATE
// for the single result_analyses row, delete-then-reinsert for citations (which
// have no natural per-row conflict key). Unlike ExecutePrompt's idempotency it
// never skips — it always overwrites, so a re-analysis pass can re-extract onto
// a bumped extraction_version even when a row already exists. A missing or
// cross-tenant result returns ErrNotFound.
func (s *Store) SaveResultAnalysis(ctx context.Context, tenantID domain.ID, params SaveResultAnalysisParams) error {
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return err
	}
	if err := validateUUIDv7("result id", params.PromptResultID); err != nil {
		return err
	}
	if params.AnalysisModel == "" {
		return errors.New("analysis model is required")
	}

	keywords := params.Keywords
	if keywords == nil {
		keywords = []string{}
	}
	excerpts := params.Excerpts
	if excerpts == nil {
		excerpts = []string{}
	}
	excerptsJSON, err := json.Marshal(excerpts)
	if err != nil {
		return fmt.Errorf("marshal excerpts: %w", err)
	}

	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		if _, err := q.ResultOwned(ctx, storesqlc.ResultOwnedParams{
			ID: params.PromptResultID, TenantID: tenantID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("verify result ownership: %w", err)
		}

		if err := q.UpsertResultAnalysis(ctx, storesqlc.UpsertResultAnalysisParams{
			PromptResultID: params.PromptResultID, Sentiment: params.Sentiment, Keywords: keywords,
			Excerpts: excerptsJSON, AnalysisModel: params.AnalysisModel,
			ExtractionVersion: int32(params.ExtractionVersion),
		}); err != nil {
			return fmt.Errorf("upsert result analysis: %w", err)
		}

		if err := q.DeleteCitationsByResult(ctx, params.PromptResultID); err != nil {
			return fmt.Errorf("delete citations: %w", err)
		}
		for _, c := range params.Citations {
			id, err := domain.NewID()
			if err != nil {
				return err
			}
			if err := q.InsertCitation(ctx, storesqlc.InsertCitationParams{
				ID: id, PromptResultID: params.PromptResultID, Url: c.URL, Domain: c.Domain,
				Title: c.Title, CiteOrder: int32(c.CiteOrder), Subject: c.Subject,
			}); err != nil {
				return fmt.Errorf("insert citation: %w", err)
			}
		}
		return nil
	})
}

// DeleteResultAnalysis clears the per-result outputs of AnalyzeResult — the
// result's result_analyses row and its citations — inside one transaction, so a
// re-run rewrites onto a clean slate (design 05, "AnalyzeResult upserts by
// prompt_result_id"). It is idempotent: a result with no analysis yet deletes
// nothing and returns nil. Cross-tenant results are invisible to the delete.
func (s *Store) DeleteResultAnalysis(ctx context.Context, tenantID, resultID domain.ID) error {
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return err
	}
	if err := validateUUIDv7("result id", resultID); err != nil {
		return err
	}

	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		if err := q.DeleteResultAnalysis(ctx, storesqlc.DeleteResultAnalysisParams{
			PromptResultID: resultID, TenantID: tenantID,
		}); err != nil {
			return fmt.Errorf("delete result analysis: %w", err)
		}
		if err := q.DeleteResultCitations(ctx, storesqlc.DeleteResultCitationsParams{
			PromptResultID: resultID, TenantID: tenantID,
		}); err != nil {
			return fmt.Errorf("delete result citations: %w", err)
		}
		return nil
	})
}

// Competitor is one competitor row as read for reconcile — the matching keys
// (name + approved aliases) plus id, status, and website. website is nil unless
// set; it feeds the LLM match pass's candidate list. suggested_aliases is
// deliberately omitted: neither pass keys off unapproved variants.
type Competitor struct {
	ID      domain.ID
	Name    string
	Website *string
	Aliases []string
	Status  string
}

// ListCompetitors returns every competitor of businessID regardless of status,
// oldest first, for reconcile's exact pass (design 05 Phase 2). Scoped to
// tenantID; a missing or cross-tenant business returns an empty slice.
func (s *Store) ListCompetitors(ctx context.Context, tenantID, businessID domain.ID) ([]Competitor, error) {
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return nil, err
	}
	if err := validateUUIDv7("business id", businessID); err != nil {
		return nil, err
	}

	rows, err := s.q(ctx).ListAnalysisCompetitors(ctx, storesqlc.ListAnalysisCompetitorsParams{
		BusinessID: businessID, TenantID: tenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("list competitors: %w", err)
	}
	competitors := make([]Competitor, 0, len(rows))
	for _, row := range rows {
		competitors = append(competitors, Competitor{
			ID: row.ID, Name: row.Name, Website: row.Website, Aliases: row.Aliases, Status: row.Status,
		})
	}
	return competitors, nil
}

// DeleteRunMentions clears every mention for a run, the wipe half of
// ReconcileEntities' delete-and-rewrite of the run's mention rows (design 05,
// step 5). Mentions reach run_id through their prompt_results join. It is
// idempotent and tenant-scoped: an unanalyzed or cross-tenant run deletes
// nothing and returns nil.
func (s *Store) DeleteRunMentions(ctx context.Context, tenantID, runID domain.ID) error {
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return err
	}
	if err := validateUUIDv7("run id", runID); err != nil {
		return err
	}

	if err := s.q(ctx).DeleteRunMentions(ctx, storesqlc.DeleteRunMentionsParams{
		ID: runID, TenantID: tenantID,
	}); err != nil {
		return fmt.Errorf("delete run mentions: %w", err)
	}
	return nil
}

// AnalyzeRunSpec is what AnalyzeRun needs to fan out: the run's owning
// business and its succeeded result ids in first-appearance order. An empty
// ResultIDs slice is valid — a run whose prompts all failed has nothing to
// analyze, which is not an error.
type AnalyzeRunSpec struct {
	BusinessID domain.ID
	ResultIDs  []domain.ID
}

// LoadAnalyzeRunSpec resolves a run's business and its succeeded result ids for
// the AnalyzeRun workflow. It is tenant-scoped: a missing or
// cross-tenant run returns ErrNotFound before any result rows are read, so a
// bad run id never leaks another tenant's results.
func (s *Store) LoadAnalyzeRunSpec(ctx context.Context, tenantID, runID domain.ID) (AnalyzeRunSpec, error) {
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return AnalyzeRunSpec{}, err
	}
	if err := validateUUIDv7("run id", runID); err != nil {
		return AnalyzeRunSpec{}, err
	}

	businessID, err := s.q(ctx).RunBusinessOwned(ctx, storesqlc.RunBusinessOwnedParams{
		ID: runID, TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AnalyzeRunSpec{}, ErrNotFound
		}
		return AnalyzeRunSpec{}, fmt.Errorf("resolve run business: %w", err)
	}

	resultIDs, err := s.q(ctx).ListSucceededResultIDs(ctx, runID)
	if err != nil {
		return AnalyzeRunSpec{}, fmt.Errorf("list succeeded results: %w", err)
	}
	return AnalyzeRunSpec{BusinessID: businessID, ResultIDs: resultIDs}, nil
}

// DiscoveredCompetitor is one still-unmatched name reconcile mints as a new
// competitor. Key is a caller-chosen correlation key (the normalized name) used
// only to link MentionWrite rows to the new competitor's minted id inside the
// transaction; it is never persisted. VerbatimName becomes both the row's name
// and its sole approved alias.
type DiscoveredCompetitor struct {
	Key          string
	VerbatimName string
}

// SuggestedAliasWrite records an LLM-proposed variant on an existing competitor
// (design 05 step 3): appended to suggested_aliases, never promoted to aliases
// (ReviewSuggestedAlias promotes an approved one). Idempotent — a variant already present
// as a suggestion or an approved alias is skipped. CommitReconcile trims the
// variant before persistence so the stored value is the exact review key.
type SuggestedAliasWrite struct {
	CompetitorID domain.ID
	Variant      string
}

// MentionWrite is one mention row for CommitReconcile. Exactly one of
// CompetitorID / DiscoveredKey is set for a competitor subject: CompetitorID
// when the entity resolved to an existing (exact or LLM) competitor,
// DiscoveredKey when it resolved to one this same commit mints (resolved to the
// new id via the Discovered map). A self subject sets neither.
type MentionWrite struct {
	PromptResultID domain.ID
	Subject        string // "self" | "competitor"
	CompetitorID   domain.ID
	DiscoveredKey  string
	MatchedBy      string // "exact" | "llm"
	MentionOrder   int
	VerbatimName   string
	Excerpt        string
}

// ReconcileCommitParams is the whole-run write payload for CommitReconcile.
type ReconcileCommitParams struct {
	RunID            domain.ID
	Discovered       []DiscoveredCompetitor
	SuggestedAliases []SuggestedAliasWrite
	Mentions         []MentionWrite
}

// CommitReconcile writes phase 2's whole-run outcome in one transaction (design
// 05 steps 4–6): mint discovered competitors, append LLM-suggested aliases,
// delete-and-rewrite the run's mentions, and stamp analysis_completed_at. Doing
// it in one transaction is the idempotency guarantee — a re-run's exact pass
// re-resolves against the competitors the previous attempt minted (reconcile
// reloads them fresh), so the delete-and-rewrite converges rather than
// duplicating. The business and run are re-verified against tenantID inside the
// transaction; a missing or cross-tenant business or run returns ErrNotFound.
func (s *Store) CommitReconcile(ctx context.Context, tenantID, businessID domain.ID, params ReconcileCommitParams) error {
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return err
	}
	if err := validateUUIDv7("business id", businessID); err != nil {
		return err
	}
	if err := validateUUIDv7("run id", params.RunID); err != nil {
		return err
	}

	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		if err := businessOwned(ctx, q, tenantID, businessID); err != nil {
			return err
		}

		// The run must belong to the same business, or a caller could stamp a run
		// they resolved for a different business.
		if _, err := q.RunOwnedByBusiness(ctx, storesqlc.RunOwnedByBusinessParams{
			ID: params.RunID, BusinessID: businessID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("verify run ownership: %w", err)
		}

		discoveredIDs := make(map[string]domain.ID, len(params.Discovered))
		for _, d := range params.Discovered {
			id, err := domain.NewID()
			if err != nil {
				return err
			}
			if err := q.InsertDiscoveredCompetitor(ctx, storesqlc.InsertDiscoveredCompetitorParams{
				ID: id, BusinessID: businessID, Name: d.VerbatimName,
			}); err != nil {
				return fmt.Errorf("insert discovered competitor: %w", err)
			}
			discoveredIDs[d.Key] = id
		}

		for _, sa := range params.SuggestedAliases {
			variant := strings.TrimSpace(sa.Variant)
			if variant == "" {
				continue
			}
			if err := q.AppendSuggestedAlias(ctx, storesqlc.AppendSuggestedAliasParams{
				ID: sa.CompetitorID, BusinessID: businessID, ArrayAppend: variant,
			}); err != nil {
				return fmt.Errorf("append suggested alias: %w", err)
			}
		}

		if err := q.DeleteRunMentions(ctx, storesqlc.DeleteRunMentionsParams{
			ID: params.RunID, TenantID: tenantID,
		}); err != nil {
			return fmt.Errorf("delete run mentions: %w", err)
		}

		for _, m := range params.Mentions {
			id, err := domain.NewID()
			if err != nil {
				return err
			}
			var competitorID *domain.ID
			if m.Subject == "competitor" {
				cid := m.CompetitorID
				if m.DiscoveredKey != "" {
					mapped, ok := discoveredIDs[m.DiscoveredKey]
					if !ok {
						return fmt.Errorf("mention references unknown discovered key %q", m.DiscoveredKey)
					}
					cid = mapped
				}
				competitorID = &cid
			}
			if err := q.InsertMention(ctx, storesqlc.InsertMentionParams{
				ID: id, PromptResultID: m.PromptResultID, Subject: m.Subject, CompetitorID: competitorID,
				MatchedBy: m.MatchedBy, MentionOrder: int32(m.MentionOrder),
				VerbatimName: &m.VerbatimName, Excerpt: m.Excerpt,
			}); err != nil {
				return fmt.Errorf("insert mention: %w", err)
			}
		}

		if err := q.SetAnalysisCompleted(ctx, storesqlc.SetAnalysisCompletedParams{
			ID: params.RunID, BusinessID: businessID,
		}); err != nil {
			return fmt.Errorf("set analysis completed: %w", err)
		}
		return nil
	})
}
