package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"opensight/internal/domain"
)

// AnalysisStore performs the wipe half of the derived-analysis tables'
// wipe-and-rebuild contract (design 02, 05). Analysis data is derived from
// prompt_results and rebuildable, so the pipeline overwrites its own outputs
// idempotently: AnalyzeResult re-runs delete a result's analysis before
// rewriting it; ReconcileEntities deletes-and-rewrites a run's mentions in one
// transaction. The raw prompt_results and monitoring_runs rows are never touched
// by these deletes.
//
// The insert/upsert surface (result_analyses, citations, competitors, mentions
// writes) is owned by the later analysis stories that define it — ANA-2 through
// ANA-6 — and deliberately not built here.
type AnalysisStore struct {
	db *sql.DB
}

// NewAnalysisStore returns an AnalysisStore backed by db.
func NewAnalysisStore(db *sql.DB) *AnalysisStore {
	return &AnalysisStore{db: db}
}

const (
	// The IN-subquery scopes each delete to the tenant through
	// prompt_results -> monitoring_runs -> businesses, so a cross-tenant caller
	// deletes nothing rather than acting as an existence oracle (design 01/02).

	deleteResultAnalysisSQL = `
DELETE FROM result_analyses
WHERE prompt_result_id = $1
  AND prompt_result_id IN (
    SELECT pr.id
    FROM prompt_results pr
    JOIN monitoring_runs r ON r.id = pr.run_id
    JOIN businesses b ON b.id = r.business_id
    WHERE b.tenant_id = $2
  )`

	deleteResultCitationsSQL = `
DELETE FROM citations
WHERE prompt_result_id = $1
  AND prompt_result_id IN (
    SELECT pr.id
    FROM prompt_results pr
    JOIN monitoring_runs r ON r.id = pr.run_id
    JOIN businesses b ON b.id = r.business_id
    WHERE b.tenant_id = $2
  )`

	deleteRunMentionsSQL = `
DELETE FROM mentions
WHERE prompt_result_id IN (
  SELECT pr.id
  FROM prompt_results pr
  JOIN monitoring_runs r ON r.id = pr.run_id
  JOIN businesses b ON b.id = r.business_id
  WHERE r.id = $1 AND b.tenant_id = $2
)`

	// resultOwnedSQL scopes a prompt_result to tenantID through
	// prompt_results -> monitoring_runs -> businesses, the same join the deletes
	// above use. It deliberately does NOT check status='succeeded': that
	// precondition is the activity's, not the store's.
	resultOwnedSQL = `
SELECT 1
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
WHERE pr.id = $1 AND b.tenant_id = $2`

	upsertResultAnalysisSQL = `
INSERT INTO result_analyses (
  prompt_result_id, sentiment, keywords, excerpts, analysis_model, extraction_version, analyzed_at
) VALUES ($1, $2, $3, $4::jsonb, $5, $6, now())
ON CONFLICT (prompt_result_id) DO UPDATE SET
  sentiment = EXCLUDED.sentiment,
  keywords = EXCLUDED.keywords,
  excerpts = EXCLUDED.excerpts,
  analysis_model = EXCLUDED.analysis_model,
  extraction_version = EXCLUDED.extraction_version,
  analyzed_at = now()`

	deleteCitationsByResultSQL = `DELETE FROM citations WHERE prompt_result_id = $1`

	insertCitationSQL = `
INSERT INTO citations (id, prompt_result_id, url, domain, title, cite_order, subject)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

	// listCompetitorsSQL loads every competitor of a business regardless of
	// status: dismissed competitors still accrue mentions, so the exact pass must
	// match against them too (design 05 Phase 2 step 2, 02: dismissal is a
	// display filter). Tenant-scoped through the businesses IN-subquery like the
	// deletes above. aliases is read as JSON (see businesses.go stringSlice).
	listCompetitorsSQL = `
SELECT id, name, to_jsonb(aliases) AS aliases, status
FROM competitors
WHERE business_id = $1
  AND business_id IN (SELECT id FROM businesses WHERE tenant_id = $2)
ORDER BY created_at`
)

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
func (s *AnalysisStore) SaveResultAnalysis(ctx context.Context, tenantID domain.ID, params SaveResultAnalysisParams) error {
	if s == nil || s.db == nil {
		return errors.New("analysis store database is required")
	}
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

	return withTx(ctx, s.db, func(q querier) error {
		var one int
		if err := q.queryRowContext(ctx, resultOwnedSQL, params.PromptResultID, tenantID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("verify result ownership: %w", err)
		}

		if _, err := q.execContext(ctx, upsertResultAnalysisSQL,
			params.PromptResultID,
			params.Sentiment,
			keywords,
			string(excerptsJSON),
			params.AnalysisModel,
			params.ExtractionVersion,
		); err != nil {
			return fmt.Errorf("upsert result analysis: %w", err)
		}

		if _, err := q.execContext(ctx, deleteCitationsByResultSQL, params.PromptResultID); err != nil {
			return fmt.Errorf("delete citations: %w", err)
		}
		for _, c := range params.Citations {
			id, err := domain.NewID()
			if err != nil {
				return err
			}
			if _, err := q.execContext(ctx, insertCitationSQL,
				id,
				params.PromptResultID,
				c.URL,
				c.Domain,
				c.Title,
				c.CiteOrder,
				c.Subject,
			); err != nil {
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
func (s *AnalysisStore) DeleteResultAnalysis(ctx context.Context, tenantID, resultID domain.ID) error {
	if s == nil || s.db == nil {
		return errors.New("analysis store database is required")
	}
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return err
	}
	if err := validateUUIDv7("result id", resultID); err != nil {
		return err
	}

	return withTx(ctx, s.db, func(q querier) error {
		if _, err := q.execContext(ctx, deleteResultAnalysisSQL, resultID, tenantID); err != nil {
			return fmt.Errorf("delete result analysis: %w", err)
		}
		if _, err := q.execContext(ctx, deleteResultCitationsSQL, resultID, tenantID); err != nil {
			return fmt.Errorf("delete result citations: %w", err)
		}
		return nil
	})
}

// Competitor is one competitor row as read for reconcile's exact pass — the
// matching keys (name + approved aliases) plus id and status. suggested_aliases
// and other columns are deliberately omitted: the exact pass keys off approved
// aliases only.
type Competitor struct {
	ID      domain.ID
	Name    string
	Aliases []string
	Status  string
}

// ListCompetitors returns every competitor of businessID regardless of status,
// oldest first, for reconcile's exact pass (design 05 Phase 2). Scoped to
// tenantID; a missing or cross-tenant business returns an empty slice.
func (s *AnalysisStore) ListCompetitors(ctx context.Context, tenantID, businessID domain.ID) ([]Competitor, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("analysis store database is required")
	}
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return nil, err
	}
	if err := validateUUIDv7("business id", businessID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, listCompetitorsSQL, businessID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list competitors: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	competitors := []Competitor{}
	for rows.Next() {
		var c Competitor
		var aliases stringSlice
		if err := rows.Scan(&c.ID, &c.Name, &aliases, &c.Status); err != nil {
			return nil, fmt.Errorf("scan competitor: %w", err)
		}
		c.Aliases = aliases
		competitors = append(competitors, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate competitors: %w", err)
	}
	return competitors, nil
}

// DeleteRunMentions clears every mention for a run, the wipe half of
// ReconcileEntities' delete-and-rewrite of the run's mention rows (design 05,
// step 5). Mentions reach run_id through their prompt_results join. It is
// idempotent and tenant-scoped: an unanalyzed or cross-tenant run deletes
// nothing and returns nil.
func (s *AnalysisStore) DeleteRunMentions(ctx context.Context, tenantID, runID domain.ID) error {
	if s == nil || s.db == nil {
		return errors.New("analysis store database is required")
	}
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return err
	}
	if err := validateUUIDv7("run id", runID); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, deleteRunMentionsSQL, runID, tenantID); err != nil {
		return fmt.Errorf("delete run mentions: %w", err)
	}
	return nil
}
