package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	JobID               int64
	StartedAt           time.Time
	CompletedAt         *time.Time
	AnalysisCompletedAt *time.Time
	// ExpectedResults is the prompt-snapshot size at run start (the "N" in
	// "k of N"). Nullable: null means "unknown," not zero.
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
	JobID           int64
	ExpectedResults int
}

// UpsertRun idempotently creates (or converges on) the run for
// (business_id, platform, scheduled_for). It is LoadRunSpec's primitive: the
// account-checked business lookup, the conflict-tolerant insert, and the
// read-back run in one transaction, so a duplicate trigger returns the
// existing run (any status) rather than erroring. A missing or cross-account
// business returns ErrNotFound.
func (s *Store) UpsertRun(ctx context.Context, accountID domain.ID, params UpsertRunParams) (Run, error) {

	params, err := normalizeUpsertRunParams(params)
	if err != nil {
		return Run{}, err
	}
	if err := validateUUIDv7("account id", accountID); err != nil {
		return Run{}, err
	}

	var run Run
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		if err := businessOwned(ctx, q, accountID, params.BusinessID); err != nil {
			return err
		}
		expected := int32(params.ExpectedResults)
		if err := q.InsertRunOnConflictNothing(ctx, storesqlc.InsertRunOnConflictNothingParams{
			ID: params.ID, BusinessID: params.BusinessID, Platform: params.Platform,
			Trigger: string(params.Trigger), ScheduledFor: params.ScheduledFor,
			JobID: params.JobID, ExpectedResults: &expected,
		}); err != nil {
			return fmt.Errorf("insert monitoring run: %w", err)
		}
		row, err := q.SelectRunByKey(ctx, storesqlc.SelectRunByKeyParams{
			BusinessID: params.BusinessID, Platform: params.Platform, ScheduledFor: params.ScheduledFor,
		})
		if err != nil {
			return fmt.Errorf("read back monitoring run: %w", err)
		}
		run = runFromFields(row.ID, row.BusinessID, row.Platform, row.Trigger, row.ScheduledFor,
			row.Status, row.JobID, row.StartedAt, row.CompletedAt, row.AnalysisCompletedAt, row.ExpectedResults)
		return nil
	})
	if err != nil {
		return Run{}, err
	}
	return run, nil
}

// FinalizeRun sets the run's terminal status from its succeeded result count
// against the run's stored expected_results (the prompt-snapshot size, so a
// prompt whose execution never wrote a row still counts against completion).
// It is account-scoped and safe under retry. A missing or cross-account run
// returns ErrNotFound.
func (s *Store) FinalizeRun(ctx context.Context, accountID, runID domain.ID) (Run, error) {

	row, err := s.q(ctx).FinalizeRun(ctx, storesqlc.FinalizeRunParams{ID: runID, AccountID: accountID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		return Run{}, fmt.Errorf("finalize run: %w", err)
	}
	return runFromFields(row.ID, row.BusinessID, row.Platform, row.Trigger, row.ScheduledFor,
		row.Status, row.JobID, row.StartedAt, row.CompletedAt, row.AnalysisCompletedAt, row.ExpectedResults), nil
}

// RunListItem is a run plus its per-run result counts, shaped like
// ResultListItem (row + derived fields). Only ListRuns populates the
// counts; other Run readers leave them zero.
type RunListItem struct {
	Run
	SucceededResults int
	FailedResults    int
	AnalyzedResults  int
}

// ListRuns returns the business's runs with per-run result counts, newest
// scheduled first. It enters through the account-checked
// business lookup so an empty result for a business the account does not own
// is ErrNotFound, not an empty slice.
func (s *Store) ListRuns(ctx context.Context, accountID, businessID domain.ID) ([]RunListItem, error) {

	q := s.q(ctx)
	if err := businessOwned(ctx, q, accountID, businessID); err != nil {
		return nil, err
	}

	rows, err := q.ListRuns(ctx, businessID)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	runs := make([]RunListItem, 0, len(rows))
	for _, row := range rows {
		item := RunListItem{Run: runFromFields(row.ID, row.BusinessID, row.Platform, row.Trigger, row.ScheduledFor,
			row.Status, row.JobID, row.StartedAt, row.CompletedAt, row.AnalysisCompletedAt, row.ExpectedResults),
			SucceededResults: int(row.Succeeded), FailedResults: int(row.Failed), AnalyzedResults: int(row.Analyzed)}
		runs = append(runs, item)
	}
	return runs, nil
}

func runFromFields(id, businessID domain.ID, platform, trigger string, scheduled time.Time, status string, jobID int64,
	started time.Time, completed, analysisCompleted *time.Time, expected *int32) Run {
	var expectedResults *int
	if expected != nil {
		v := int(*expected)
		expectedResults = &v
	}
	return Run{ID: id, BusinessID: businessID, Platform: platform, Trigger: RunTrigger(trigger),
		ScheduledFor: scheduled, Status: RunStatus(status), JobID: jobID, StartedAt: started,
		CompletedAt: completed, AnalysisCompletedAt: analysisCompleted, ExpectedResults: expectedResults}
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
	if params.JobID < 1 {
		return UpsertRunParams{}, errors.New("run job_id must be positive")
	}
	if params.ExpectedResults < 0 {
		return UpsertRunParams{}, errors.New("run expected_results must not be negative")
	}
	return params, nil
}
