package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/domain"

	"github.com/google/uuid"
)

// RunStatus is the persisted status of a monitoring run.
type RunStatus string

const (
	// RunStatusRunning is a run in progress.
	RunStatusRunning RunStatus = "running"
	// RunStatusCompleted is a run where every expected result succeeded.
	RunStatusCompleted RunStatus = "completed"
	// RunStatusPartial is a run where some but not all results succeeded.
	RunStatusPartial RunStatus = "partial"
	// RunStatusFailed is a run where no result succeeded.
	RunStatusFailed RunStatus = "failed"
)

// PlatformChatGPT is the only monitored platform in Phase 1 (design 04).
const PlatformChatGPT = "chatgpt"

// RunTrigger is what caused a run to be created.
type RunTrigger string

const (
	// RunTriggerInitial is the onboarding first run.
	RunTriggerInitial RunTrigger = "initial"
	// RunTriggerScheduled is a run created by the recurring schedule.
	RunTriggerScheduled RunTrigger = "scheduled"
	// RunTriggerManual is an operator- or user-triggered run.
	RunTriggerManual RunTrigger = "manual"
)

// Run is a persisted monitoring_runs row (migration 00004).
type Run struct {
	ID                  domain.ID
	BusinessID          domain.ID
	Platform            string
	Trigger             RunTrigger
	ScheduledFor        time.Time
	Status              RunStatus
	WorkflowID          string
	StartedAt           time.Time
	CompletedAt         *time.Time
	AnalysisCompletedAt *time.Time
	// ExpectedResults is the prompt-snapshot size at run start (the "N" in
	// "k of N"). Nullable: runs created before RUNS-1 stay null, never
	// backfilled — null means "unknown," not zero.
	ExpectedResults *int
}

// UpsertRunParams are the inputs for UpsertRun. If ID is uuid.Nil a UUIDv7 is
// generated for the potential insert. ScheduledFor is stored as a date.
type UpsertRunParams struct {
	ID              domain.ID
	BusinessID      domain.ID
	Platform        string
	Trigger         RunTrigger
	ScheduledFor    time.Time
	WorkflowID      string
	ExpectedResults int
}

// RunStore reads and writes monitoring_runs rows.
type RunStore struct {
	db *sql.DB
}

// NewRunStore returns a RunStore backed by db.
func NewRunStore(db *sql.DB) *RunStore {
	return &RunStore{db: db}
}

const (
	runColumns = `id, business_id, platform, trigger, scheduled_for, status,
       workflow_id, started_at, completed_at, analysis_completed_at, expected_results`

	insertRunOnConflictNothingSQL = `
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, expected_results)
VALUES ($1, $2, $3, $4, $5, 'running', $6, $7)
ON CONFLICT (business_id, platform, scheduled_for) DO NOTHING`

	selectRunByKeySQL = `
SELECT ` + runColumns + `
FROM monitoring_runs
WHERE business_id = $1 AND platform = $2 AND scheduled_for = $3`

	// finalizeRunSQL recomputes the terminal status from the run's succeeded
	// result count (design 04): succeeded == 0 -> failed; succeeded == expected
	// -> completed; else partial. expected_results is read from the row rather
	// than passed in (RUNS-1); a legacy null row falls back to the observed
	// total so an in-flight-at-deploy run still finalizes sensibly. The failed
	// branch is checked first so a zero-prompt run (expected == succeeded == 0)
	// is failed, not completed. It is tenant-scoped via the businesses join and
	// safe to re-run under activity retry (a pure recomputation). 0 rows
	// updated -> ErrNotFound.
	finalizeRunSQL = `
UPDATE monitoring_runs r
SET status = CASE
      WHEN sub.succeeded = 0 THEN 'failed'
      WHEN sub.succeeded = COALESCE(r.expected_results, sub.total) THEN 'completed'
      ELSE 'partial'
    END,
    completed_at = now()
FROM businesses b,
     (SELECT count(*) FILTER (WHERE status = 'succeeded') AS succeeded,
             count(*) AS total
      FROM prompt_results WHERE run_id = $1) sub
WHERE r.id = $1
  AND r.business_id = b.id
  AND b.tenant_id = $2
RETURNING ` + runColumnsPrefixed

	runColumnsPrefixed = `r.id, r.business_id, r.platform, r.trigger, r.scheduled_for, r.status,
       r.workflow_id, r.started_at, r.completed_at, r.analysis_completed_at, r.expected_results`

	listRunsSQL = `
SELECT ` + runColumns + `
FROM monitoring_runs
WHERE business_id = $1
ORDER BY scheduled_for DESC`
)

// UpsertRun idempotently creates (or converges on) the run for
// (business_id, platform, scheduled_for). It is LoadRunSpec's primitive
// (RUN-3): the tenant-checked business lookup, the conflict-tolerant insert, and
// the read-back run in one transaction, so a duplicate trigger returns the
// existing run (any status) rather than erroring. A missing or cross-tenant
// business returns ErrNotFound.
func (s *RunStore) UpsertRun(ctx context.Context, tenantID domain.ID, params UpsertRunParams) (Run, error) {
	if s == nil || s.db == nil {
		return Run{}, errors.New("run store database is required")
	}

	params, err := normalizeUpsertRunParams(params)
	if err != nil {
		return Run{}, err
	}
	if err := validateUUIDv7("tenant id", tenantID); err != nil {
		return Run{}, err
	}

	var run Run
	err = withTx(ctx, s.db, func(q querier) error {
		if err := businessOwned(ctx, q, tenantID, params.BusinessID); err != nil {
			return err
		}
		if _, err := q.execContext(
			ctx,
			insertRunOnConflictNothingSQL,
			params.ID,
			params.BusinessID,
			params.Platform,
			string(params.Trigger),
			params.ScheduledFor,
			params.WorkflowID,
			params.ExpectedResults,
		); err != nil {
			return fmt.Errorf("insert monitoring run: %w", err)
		}
		loaded, err := scanRun(q.queryRowContext(
			ctx,
			selectRunByKeySQL,
			params.BusinessID,
			params.Platform,
			params.ScheduledFor,
		))
		if err != nil {
			return fmt.Errorf("read back monitoring run: %w", err)
		}
		run = loaded
		return nil
	})
	if err != nil {
		return Run{}, err
	}
	return run, nil
}

// FinalizeRun sets the run's terminal status from its succeeded result count
// against the run's stored expected_results (the prompt-snapshot size, so a
// prompt whose activity never wrote a row still counts against completion).
// It is tenant-scoped and safe under retry. A missing or cross-tenant run
// returns ErrNotFound.
func (s *RunStore) FinalizeRun(ctx context.Context, tenantID, runID domain.ID) (Run, error) {
	if s == nil || s.db == nil {
		return Run{}, errors.New("run store database is required")
	}

	run, err := scanRun(s.db.QueryRowContext(ctx, finalizeRunSQL, runID, tenantID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		return Run{}, fmt.Errorf("finalize run: %w", err)
	}
	return run, nil
}

// ListRuns returns the business's runs, newest scheduled first (WEB-2/WEB-5). It
// enters through the tenant-checked business lookup so an empty result for a
// business the tenant does not own is ErrNotFound, not an empty slice.
func (s *RunStore) ListRuns(ctx context.Context, tenantID, businessID domain.ID) ([]Run, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("run store database is required")
	}

	q := sqlQuerier{q: s.db}
	if err := businessOwned(ctx, q, tenantID, businessID); err != nil {
		return nil, err
	}

	rows, err := q.queryContext(ctx, listRunsSQL, businessID)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	runs := []Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs: %w", err)
	}
	return runs, nil
}

func scanRun(row rowScanner) (Run, error) {
	var run Run
	if err := row.Scan(
		&run.ID,
		&run.BusinessID,
		&run.Platform,
		&run.Trigger,
		&run.ScheduledFor,
		&run.Status,
		&run.WorkflowID,
		&run.StartedAt,
		&run.CompletedAt,
		&run.AnalysisCompletedAt,
		&run.ExpectedResults,
	); err != nil {
		return Run{}, err
	}
	return run, nil
}

func normalizeUpsertRunParams(params UpsertRunParams) (UpsertRunParams, error) {
	if params.ID == uuid.Nil {
		id, err := domain.NewID()
		if err != nil {
			return UpsertRunParams{}, err
		}
		params.ID = id
	}
	if err := validateUUIDv7("run id", params.ID); err != nil {
		return UpsertRunParams{}, err
	}
	if err := validateUUIDv7("business id", params.BusinessID); err != nil {
		return UpsertRunParams{}, err
	}
	if strings.TrimSpace(params.Platform) == "" {
		return UpsertRunParams{}, errors.New("run platform is required")
	}
	switch params.Trigger {
	case RunTriggerInitial, RunTriggerScheduled, RunTriggerManual:
	default:
		return UpsertRunParams{}, fmt.Errorf("run trigger must be one of initial, scheduled, manual")
	}
	if params.ScheduledFor.IsZero() {
		return UpsertRunParams{}, errors.New("run scheduled_for is required")
	}
	if strings.TrimSpace(params.WorkflowID) == "" {
		return UpsertRunParams{}, errors.New("run workflow_id is required")
	}
	if params.ExpectedResults < 0 {
		return UpsertRunParams{}, errors.New("run expected_results must not be negative")
	}
	return params, nil
}
