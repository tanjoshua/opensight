package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"
	"opensight/internal/visibility"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FindingStatus is the whole lifecycle. Activeness is derived from whether the
// current audit still reproduces the finding, so there is no retired or
// superseded state to keep in step.
type FindingStatus string

const (
	FindingOpen      FindingStatus = "OPEN"
	FindingDone      FindingStatus = "DONE"
	FindingDismissed FindingStatus = "DISMISSED"
)

var ErrInvalidFindingTransition = errors.New("invalid finding transition")

// SiteAudit is one published run of the check catalog.
type SiteAudit struct {
	ID        domain.ID
	CheckedAt time.Time
	PagesRead int
	Failure   string
	Checks    []visibility.CheckResult
}

// FindingRecord is one row of the work queue, carrying the finder's output plus
// the user's own decision about it.
type FindingRecord struct {
	ID              domain.ID
	Key, Source     string
	Category        string
	Title, Body     string
	Steps           []string
	Detail          string
	ResultIDs       []domain.ID
	PromptIDs       []domain.ID
	Sources         []string
	Blocking        bool
	Reach, Priority int
	Comparison      visibility.Comparison
	Status          FindingStatus
	DismissalReason *string
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	CompletedAt     *time.Time
	DismissedAt     *time.Time
}

// ImproveRun is everything one monitoring run produced for the Improve feature.
// It is published as a unit: either the audit and the findings both land, or the
// previous ones stay current and nothing is half-updated.
type ImproveRun struct {
	RunID     domain.ID
	PagesRead int
	Failure   string
	Checks    []visibility.CheckResult
	Findings  []visibility.Finding
}

// PublishImproveRun is the single write boundary. One transaction inserts the
// audit, makes it current, and refreshes every finding the run produced. A
// finding the run did not reproduce is left exactly as it is: it simply stops
// being active, because ListFindings reads the current audit's timestamp rather
// than any per-finding state.
//
// Re-running the same monitoring run is a no-op: the audit insert conflicts on
// monitoring_run_id and the transaction returns without touching published state.
func (s *Store) PublishImproveRun(ctx context.Context, accountID, businessID domain.ID, run ImproveRun) error {
	checks, err := json.Marshal(run.Checks)
	if err != nil {
		return err
	}
	newAuditID, err := domain.NewID()
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		auditID, err := q.InsertSiteAudit(ctx, storesqlc.InsertSiteAuditParams{
			ID:              newAuditID,
			AccountID:       accountID,
			BusinessID:      businessID,
			MonitoringRunID: run.RunID,
			PagesRead:       int32(run.PagesRead),
			Failure:         nullableText(run.Failure),
			Checks:          checks,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// Either this run was already published, or it is not a run of this
			// business. Both mean: leave published state alone.
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.PublishSiteAudit(ctx, storesqlc.PublishSiteAuditParams{ID: auditID, BusinessID: businessID}); err != nil {
			return err
		}

		for _, finding := range run.Findings {
			params, err := findingParams(accountID, businessID, finding)
			if err != nil {
				return err
			}
			if err := q.UpsertFinding(ctx, params); err != nil {
				return err
			}
		}
		return nil
	})
}

func findingParams(accountID, businessID domain.ID, f visibility.Finding) (storesqlc.UpsertFindingParams, error) {
	steps, err := json.Marshal(nonNil(f.Steps))
	if err != nil {
		return storesqlc.UpsertFindingParams{}, err
	}
	resultIDs, err := idsFromStrings(f.ResultIDs)
	if err != nil {
		return storesqlc.UpsertFindingParams{}, err
	}
	promptIDs, err := idsFromStrings(f.PromptIDs)
	if err != nil {
		return storesqlc.UpsertFindingParams{}, err
	}
	comparison, err := json.Marshal(f.Comparison)
	if err != nil {
		return storesqlc.UpsertFindingParams{}, err
	}
	id, err := domain.NewID()
	if err != nil {
		return storesqlc.UpsertFindingParams{}, err
	}
	return storesqlc.UpsertFindingParams{
		ID:         id,
		AccountID:  accountID,
		BusinessID: businessID,
		Key:        f.Key,
		Source:     f.Source,
		Category:   f.Category,
		Title:      f.Title,
		Body:       f.Body,
		Steps:      steps,
		Detail:     f.Detail,
		ResultIds:  resultIDs,
		PromptIds:  promptIDs,
		Sources:    nonNil(f.Sources),
		Blocking:   f.Blocking,
		Reach:      int32(f.Reach),
		Priority:   int32(f.Priority),
		Comparison: comparison,
	}, nil
}

// PublishedAudit returns the business's current audit. A business with no
// successful run yet has none, which the checklist reports as not yet assessed
// rather than as a healthy result.
func (s *Store) PublishedAudit(ctx context.Context, accountID, businessID domain.ID) (SiteAudit, bool, error) {
	row, err := s.q(ctx).GetPublishedSiteAudit(ctx, storesqlc.GetPublishedSiteAuditParams{BusinessID: businessID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return SiteAudit{}, false, nil
	}
	if err != nil {
		return SiteAudit{}, false, err
	}
	audit := SiteAudit{ID: row.ID, CheckedAt: row.CheckedAt, PagesRead: int(row.PagesRead)}
	if row.Failure != nil {
		audit.Failure = *row.Failure
	}
	if err := json.Unmarshal(row.Checks, &audit.Checks); err != nil {
		return SiteAudit{}, false, err
	}
	return audit, true, nil
}

func (s *Store) ListFindings(ctx context.Context, accountID, businessID domain.ID) ([]FindingRecord, error) {
	if err := businessOwned(ctx, s.q(ctx), accountID, businessID); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListFindings(ctx, storesqlc.ListFindingsParams{
		BusinessID: businessID, AccountID: accountID, CategoryOrder: visibility.CategoryKeys(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]FindingRecord, 0, len(rows))
	for _, row := range rows {
		record, err := findingRecord(row)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *Store) GetFinding(ctx context.Context, accountID, id domain.ID) (FindingRecord, error) {
	row, err := s.q(ctx).GetFinding(ctx, storesqlc.GetFindingParams{ID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return FindingRecord{}, ErrNotFound
	}
	if err != nil {
		return FindingRecord{}, err
	}
	return findingRecord(storesqlc.Finding(row))
}

// SetFindingStatus applies a user decision. Completing and dismissing are the
// two forward moves; either can be undone back to open, which is what makes an
// accidental dismissal recoverable without a restore concept of its own.
func (s *Store) SetFindingStatus(ctx context.Context, accountID, id domain.ID, status FindingStatus, reason *string) (FindingRecord, error) {
	current, err := s.GetFinding(ctx, accountID, id)
	if err != nil {
		return FindingRecord{}, err
	}
	allowed := map[FindingStatus][]FindingStatus{
		FindingOpen:      {FindingDone, FindingDismissed},
		FindingDone:      {FindingOpen},
		FindingDismissed: {FindingOpen},
	}
	permitted := false
	for _, next := range allowed[current.Status] {
		if next == status {
			permitted = true
		}
	}
	if !permitted {
		return FindingRecord{}, ErrInvalidFindingTransition
	}
	if status == FindingDismissed && (reason == nil || strings.TrimSpace(*reason) == "") {
		return FindingRecord{}, ErrInvalidFindingTransition
	}
	if status != FindingDismissed {
		reason = nil
	}
	row, err := s.q(ctx).SetFindingStatus(ctx, storesqlc.SetFindingStatusParams{
		ID: id, AccountID: accountID,
		Status: string(status), PreviousStatus: string(current.Status), DismissalReason: reason,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return FindingRecord{}, ErrInvalidFindingTransition
	}
	if err != nil {
		return FindingRecord{}, err
	}
	return findingRecord(storesqlc.Finding(row))
}

func findingRecord(row storesqlc.Finding) (FindingRecord, error) {
	record := FindingRecord{
		ID: row.ID, Key: row.Key, Source: row.Source, Category: row.Category,
		Title: row.Title, Body: row.Body,
		Detail: row.Detail, Sources: row.Sources, Blocking: row.Blocking,
		Reach: int(row.Reach), Priority: int(row.Priority), Status: FindingStatus(row.Status),
		DismissalReason: row.DismissalReason, FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
		CompletedAt: row.CompletedAt, DismissedAt: row.DismissedAt,
	}
	if err := json.Unmarshal(row.Steps, &record.Steps); err != nil {
		return FindingRecord{}, err
	}
	if err := json.Unmarshal(row.Comparison, &record.Comparison); err != nil {
		return FindingRecord{}, err
	}
	for _, id := range row.ResultIds {
		record.ResultIDs = append(record.ResultIDs, domain.ID(id))
	}
	for _, id := range row.PromptIds {
		record.PromptIDs = append(record.PromptIDs, domain.ID(id))
	}
	return record, nil
}

func (s *Store) LoadMonitoringSnapshot(ctx context.Context, accountID, businessID domain.ID) (visibility.MonitoringSnapshot, error) {
	business, err := s.GetBusiness(ctx, accountID, businessID)
	if err != nil {
		return visibility.MonitoringSnapshot{}, err
	}
	rows, err := s.q(ctx).LoadMonitoringEvidence(ctx, storesqlc.LoadMonitoringEvidenceParams{BusinessID: businessID, AccountID: accountID})
	if err != nil {
		return visibility.MonitoringSnapshot{}, err
	}
	host := ""
	if business.Website != nil {
		if parsed, e := url.Parse(*business.Website); e == nil {
			host = strings.ToLower(parsed.Hostname())
		}
	}
	snapshot := visibility.MonitoringSnapshot{PayloadVersion: 1, BusinessName: business.Name, WebsiteHost: host}
	indexes := map[domain.ID]int{}
	for _, row := range rows {
		i, ok := indexes[row.RunID]
		if !ok {
			i = len(snapshot.Runs)
			indexes[row.RunID] = i
			snapshot.Runs = append(snapshot.Runs, visibility.SnapshotRun{RunID: row.RunID.String()})
		}
		text := ""
		if row.ResponseText != nil {
			text = *row.ResponseText
		}
		var citations []visibility.SnapshotCitation
		if err := json.Unmarshal(row.Citations, &citations); err != nil {
			return visibility.MonitoringSnapshot{}, fmt.Errorf("decode citations for result %s: %w", row.ResultID, err)
		}
		snapshot.Runs[i].Results = append(snapshot.Runs[i].Results, visibility.SnapshotResult{ResultID: row.ResultID.String(), PromptID: row.PromptID.String(), Prompt: row.Prompt, ResponseText: text, Mentioned: row.Mentioned, Citations: citations})
	}
	return snapshot, nil
}

func idsFromStrings(values []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nullableText(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
