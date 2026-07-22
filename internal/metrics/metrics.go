package metrics

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"opensight/internal/domain"
)

// Metrics computes the shared product metrics over the design-02 tables.
type Metrics struct {
	db *sql.DB
}

// New returns a Metrics backed by db.
func New(db *sql.DB) *Metrics {
	return &Metrics{db: db}
}

// analyzedJoin is the metrics base: prompt_results joined up to their run and
// business, gated to results that have a result_analyses row. analyzedWhere adds
// the tenant scope and the analysis_completed_at gate. Splitting the two lets a
// query slot extra joins (mentions, citations, unnest) between them before the
// WHERE. Together they are the single definition of "an analyzed result" that
// every metric shares, so no section can gate differently.
const (
	analyzedJoin = `
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
JOIN result_analyses ra ON ra.prompt_result_id = pr.id`

	analyzedWhere = `
WHERE b.id = $1 AND b.tenant_id = $2 AND r.analysis_completed_at IS NOT NULL`

	// selfMentions is the distinct set of results carrying a self mention, kept as
	// a subquery so a result with more than one self mention still counts once.
	selfMentions = `
LEFT JOIN (SELECT DISTINCT prompt_result_id FROM mentions WHERE subject = 'self') sm
       ON sm.prompt_result_id = pr.id`
)

func (m *Metrics) ready() error {
	if m == nil || m.db == nil {
		return errors.New("metrics database is required")
	}
	return nil
}

// percent returns num/denom as a percentage, guarding a zero denominator.
func percent(num, denom int) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom) * 100
}

// resultIDs scans to_jsonb(array_agg(id)) — a JSON array of UUID strings — into
// []domain.ID. The pgx database/sql driver will not decode a uuid[] back into
// []uuid.UUID, so ids are aggregated as jsonb and unmarshalled here, mirroring
// store.stringSlice.
type resultIDs []domain.ID

func (r *resultIDs) Scan(src any) error {
	if src == nil {
		*r = nil
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("unsupported source type %T for result ids", src)
	}
	var ids []domain.ID
	if err := json.Unmarshal(raw, &ids); err != nil {
		return fmt.Errorf("unmarshal result ids: %w", err)
	}
	*r = ids
	return nil
}
