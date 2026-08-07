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

type ModulePlanEntry struct {
	AssessorKey                      string `json:"assessor_key"`
	ModuleVersion                    int    `json:"module_version"`
	PracticeKeys, RequiredCollectors []string
}

type AssessmentGeneration struct {
	ID     domain.ID
	Status string
}

func (s *Store) StartAssessmentGeneration(ctx context.Context, accountID, businessID, runID domain.ID, plan []ModulePlanEntry) (AssessmentGeneration, error) {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return AssessmentGeneration{}, err
	}
	id, err := domain.NewID()
	if err != nil {
		return AssessmentGeneration{}, err
	}
	row, err := s.q(ctx).UpsertAssessmentGeneration(ctx, storesqlc.UpsertAssessmentGenerationParams{ID: id, AccountID: accountID, BusinessID: businessID, MonitoringRunID: runID, CompilerVersion: visibility.CompilerVersion, RankerVersion: visibility.RankerVersion, ModulePlan: planJSON})
	if errors.Is(err, pgx.ErrNoRows) {
		return AssessmentGeneration{}, ErrNotFound
	}
	if err != nil {
		return AssessmentGeneration{}, fmt.Errorf("start assessment generation: %w", err)
	}
	return AssessmentGeneration{ID: row.ID, Status: row.Status}, nil
}

func (s *Store) SaveEvidenceArtifact(ctx context.Context, generationID, accountID domain.ID, a visibility.EvidenceArtifact, artifactErr error) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	status := "SUCCEEDED"
	var errText *string
	if artifactErr != nil {
		status = "FAILED"
		v := artifactErr.Error()
		errText = &v
	}
	p := a.Payload
	if len(p) == 0 {
		p = json.RawMessage(`{}`)
	}
	return s.q(ctx).UpsertEvidenceArtifact(ctx, storesqlc.UpsertEvidenceArtifactParams{ID: id, GenerationID: generationID, AccountID: accountID, CollectorKey: a.CollectorKey, CollectorVersion: int32(a.CollectorVersion), PayloadVersion: int32(a.PayloadVersion), Status: status, CheckedAt: a.CheckedAt, Payload: p, Error: errText})
}

func idsFromStrings(values []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(values))
	for _, v := range values {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, fmt.Errorf("invalid evidence id %q: %w", v, err)
		}
		out = append(out, id)
	}
	return out, nil
}

func (s *Store) SaveAssessment(ctx context.Context, generationID, accountID, businessID domain.ID, d visibility.AssessmentDraft) (domain.ID, error) {
	resultIDs, err := idsFromStrings(d.ResultIDs)
	if err != nil {
		return uuid.Nil, err
	}
	promptIDs, err := idsFromStrings(d.PromptIDs)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := domain.NewID()
	if err != nil {
		return uuid.Nil, err
	}
	stored, err := s.q(ctx).UpsertVisibilityAssessment(ctx, storesqlc.UpsertVisibilityAssessmentParams{ID: id, GenerationID: generationID, AccountID: accountID, BusinessID: businessID, PracticeKey: d.PracticeKey, CriteriaVersion: int32(d.CriteriaVersion), AssessorKey: d.AssessorKey, AssessorVersion: int32(d.AssessorVersion), SubjectKey: d.SubjectKey, Status: string(d.Status), ResultIds: resultIDs, PromptIds: promptIDs, CheckedSources: d.CheckedSources, Explanation: d.Explanation, Reach: int32(d.Reach), Persistence: int32(d.Persistence), EvidenceQuality: int32(d.EvidenceQuality), Actionability: int32(d.Actionability), Effort: int32(d.Effort), PayloadVersion: int32(d.PayloadVersion), Payload: d.Payload})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("save assessment: %w", err)
	}
	return stored, nil
}

// CompiledOpportunity is one ranked item the compiler produced for a
// generation: a compiled assessment plus the id it was persisted under. Rank is
// its position in the slice passed to RebuildOpportunities.
type CompiledOpportunity struct {
	AssessmentID domain.ID
	visibility.CompiledAssessment
}

// RebuildOpportunities replaces the derived half of a business's opportunities
// — rank, presentation and the current assessment/generation pointers — with
// the items this generation compiled, and drops every other row out of the
// current set. The user-owned half (status, dismissal reason, completion
// baseline, first_seen_at) is never touched, and a row that stops being current
// keeps its last-known presentation so acted-on history still renders.
func (s *Store) RebuildOpportunities(ctx context.Context, accountID, businessID, generationID domain.ID, items []CompiledOpportunity) error {
	prepared := make([]storesqlc.UpsertOpportunityParams, 0, len(items))
	for i, item := range items {
		raw, err := json.Marshal(item.Presentation)
		if err != nil {
			return err
		}
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		prepared = append(prepared, storesqlc.UpsertOpportunityParams{ID: id, AccountID: accountID, BusinessID: businessID, PracticeKey: item.Draft.PracticeKey, SubjectKey: item.Draft.SubjectKey, CurrentAssessmentID: item.AssessmentID, CurrentGenerationID: generationID, Rank: int32(i + 1), Presentation: raw})
	}
	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		for _, params := range prepared {
			if _, err := q.UpsertOpportunity(ctx, params); errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			} else if err != nil {
				return fmt.Errorf("save opportunity: %w", err)
			}
		}
		if err := q.SweepStaleOpportunities(ctx, storesqlc.SweepStaleOpportunitiesParams{BusinessID: businessID, AccountID: accountID, CurrentGenerationID: generationID}); err != nil {
			return fmt.Errorf("sweep stale opportunities: %w", err)
		}
		return nil
	})
}

type CompletedOutcomeCandidate struct {
	ID                      domain.ID
	PracticeKey, SubjectKey string
	Assessment              visibility.AssessmentDraft
}

func (s *Store) ListCompletedOutcomeCandidates(ctx context.Context, generationID, accountID, businessID domain.ID) ([]CompletedOutcomeCandidate, error) {
	rows, err := s.q(ctx).ListCompletedGenerationOpportunities(ctx, storesqlc.ListCompletedGenerationOpportunitiesParams{AccountID: accountID, BusinessID: businessID, GenerationID: generationID})
	if err != nil {
		return nil, err
	}
	out := make([]CompletedOutcomeCandidate, 0, len(rows))
	for _, row := range rows {
		resultIDs := make([]string, 0, len(row.ResultIds))
		for _, id := range row.ResultIds {
			resultIDs = append(resultIDs, id.String())
		}
		promptIDs := make([]string, 0, len(row.PromptIds))
		for _, id := range row.PromptIds {
			promptIDs = append(promptIDs, id.String())
		}
		out = append(out, CompletedOutcomeCandidate{ID: row.ID, PracticeKey: row.PracticeKey, SubjectKey: row.SubjectKey, Assessment: visibility.AssessmentDraft{PracticeKey: row.PracticeKey, SubjectKey: row.SubjectKey, Status: visibility.AssessmentStatus(row.AssessmentStatus), ResultIDs: resultIDs, PromptIDs: promptIDs}})
	}
	return out, nil
}

func (s *Store) AppendOutcomeObservation(ctx context.Context, accountID, opportunityID domain.ID, eventKey string, payload json.RawMessage) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	return s.q(ctx).InsertOpportunityEvent(ctx, storesqlc.InsertOpportunityEventParams{ID: id, OpportunityID: opportunityID, AccountID: accountID, EventKey: eventKey, EventType: "OUTCOME_OBSERVED", Payload: payload})
}

func (s *Store) FinishAssessmentGeneration(ctx context.Context, generationID, accountID domain.ID, status string, generationErr error) error {
	var msg *string
	if generationErr != nil {
		v := generationErr.Error()
		msg = &v
	}
	return s.q(ctx).FinishAssessmentGeneration(ctx, storesqlc.FinishAssessmentGenerationParams{ID: generationID, AccountID: accountID, Status: status, Error: msg})
}

func (s *Store) LoadMonitoringSnapshot(ctx context.Context, accountID, businessID domain.ID) (visibility.MonitoringSnapshot, error) {
	business, err := s.GetBusiness(ctx, accountID, businessID)
	if err != nil {
		return visibility.MonitoringSnapshot{}, err
	}
	rows, err := s.q(ctx).LoadMonitoringEvidence(ctx, storesqlc.LoadMonitoringEvidenceParams{BusinessID: businessID, AccountID: accountID})
	if err != nil {
		return visibility.MonitoringSnapshot{}, fmt.Errorf("load monitoring evidence: %w", err)
	}
	host := ""
	if business.Website != nil {
		if u, e := urlParse(*business.Website); e == nil {
			host = u
		}
	}
	snap := visibility.MonitoringSnapshot{PayloadVersion: 1, BusinessName: business.Name, WebsiteHost: host}
	runIndex := map[domain.ID]int{}
	for _, r := range rows {
		i, ok := runIndex[r.RunID]
		if !ok {
			i = len(snap.Runs)
			runIndex[r.RunID] = i
			snap.Runs = append(snap.Runs, visibility.SnapshotRun{RunID: r.RunID.String()})
		}
		text := ""
		if r.ResponseText != nil {
			text = *r.ResponseText
		}
		snap.Runs[i].Results = append(snap.Runs[i].Results, visibility.SnapshotResult{ResultID: r.ResultID.String(), PromptID: r.PromptID.String(), Prompt: r.Prompt, ResponseText: text, Mentioned: r.Mentioned, Competitors: r.Competitors, CitationDomains: r.CitationDomains, CitationURLs: r.CitationUrls})
	}
	return snap, nil
}

func urlParse(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	return strings.ToLower(u.Hostname()), nil
}

type OpportunityStatus string

const (
	StatusOpen       OpportunityStatus = "OPEN"
	StatusInProgress OpportunityStatus = "IN_PROGRESS"
	StatusCompleted  OpportunityStatus = "COMPLETED"
	StatusDismissed  OpportunityStatus = "DISMISSED"
)

type OpportunityRecord struct {
	ID, BusinessID          domain.ID
	PracticeKey, SubjectKey string
	Rank                    int
	Presentation            visibility.Presentation
	UserStatus              OpportunityStatus
	DismissalReason         *string
	CompletionBaseline      json.RawMessage
	// Current reports whether the newest assessment generation still produced
	// this opportunity; Focus is the single definition of a focus slot —
	// current, actionable, and among the top three by rank.
	Current, Focus                     bool
	FirstSeenAt, UpdatedAt, AssessedAt time.Time
	AssessmentStatus                   visibility.AssessmentStatus
	ResultIDs, PromptIDs               []domain.ID
	CheckedSources                     []string
	Explanation                        string
	Observations                       []OpportunityObservation
}

type OpportunityObservation struct {
	AssessmentStatus visibility.AssessmentStatus
	ObservedAt       time.Time
	ResultIDs        []string
	PromptIDs        []string
}

func decodeObservation(raw json.RawMessage, observedAt time.Time) (OpportunityObservation, error) {
	var payload struct {
		AssessmentStatus visibility.AssessmentStatus `json:"assessment_status"`
		ResultIDs        []string                    `json:"result_ids"`
		PromptIDs        []string                    `json:"prompt_ids"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return OpportunityObservation{}, fmt.Errorf("decode outcome observation: %w", err)
	}
	return OpportunityObservation{AssessmentStatus: payload.AssessmentStatus, ObservedAt: observedAt, ResultIDs: payload.ResultIDs, PromptIDs: payload.PromptIDs}, nil
}

func (s *Store) loadOpportunityObservations(ctx context.Context, accountID, opportunityID domain.ID) ([]OpportunityObservation, error) {
	rows, err := s.q(ctx).ListOpportunityOutcomeEvents(ctx, storesqlc.ListOpportunityOutcomeEventsParams{OpportunityID: opportunityID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	out := make([]OpportunityObservation, 0, len(rows))
	for _, row := range rows {
		observation, err := decodeObservation(row.Payload, row.CreatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, observation)
	}
	return out, nil
}

// loadBusinessObservations fetches every card's observations in one query, so a
// business with N opportunities costs two queries rather than N+1.
func (s *Store) loadBusinessObservations(ctx context.Context, accountID, businessID domain.ID) (map[domain.ID][]OpportunityObservation, error) {
	rows, err := s.q(ctx).ListBusinessOutcomeEvents(ctx, storesqlc.ListBusinessOutcomeEventsParams{BusinessID: businessID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	out := map[domain.ID][]OpportunityObservation{}
	for _, row := range rows {
		observation, err := decodeObservation(row.Payload, row.CreatedAt)
		if err != nil {
			return nil, err
		}
		out[row.OpportunityID] = append(out[row.OpportunityID], observation)
	}
	return out, nil
}

func opportunityFromFields(id, businessID domain.ID, practice, subject string, rank int32, presentation json.RawMessage, userStatus string, dismissal *string, baseline *json.RawMessage, current, focus bool, first, updated time.Time, assessment string, resultIDs, promptIDs []uuid.UUID, sources []string, explanation string, assessed time.Time) (OpportunityRecord, error) {
	var p visibility.Presentation
	if err := json.Unmarshal(presentation, &p); err != nil {
		return OpportunityRecord{}, err
	}
	var b json.RawMessage
	if baseline != nil {
		b = *baseline
	}
	return OpportunityRecord{ID: id, BusinessID: businessID, PracticeKey: practice, SubjectKey: subject, Rank: int(rank), Presentation: p, UserStatus: OpportunityStatus(userStatus), DismissalReason: dismissal, CompletionBaseline: b, Current: current, Focus: focus, FirstSeenAt: first, UpdatedAt: updated, AssessmentStatus: visibility.AssessmentStatus(assessment), ResultIDs: resultIDs, PromptIDs: promptIDs, CheckedSources: sources, Explanation: explanation, AssessedAt: assessed}, nil
}

func (s *Store) ListOpportunities(ctx context.Context, accountID, businessID domain.ID) ([]OpportunityRecord, error) {
	if err := businessOwned(ctx, s.q(ctx), accountID, businessID); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListCurrentOpportunities(ctx, storesqlc.ListCurrentOpportunitiesParams{BusinessID: businessID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	observations, err := s.loadBusinessObservations(ctx, accountID, businessID)
	if err != nil {
		return nil, err
	}
	out := make([]OpportunityRecord, 0, len(rows))
	for _, r := range rows {
		o, e := opportunityFromFields(r.ID, businessID, r.PracticeKey, r.SubjectKey, r.Rank, r.Presentation, r.UserStatus, r.DismissalReason, r.CompletionBaseline, r.Current, r.Focus, r.FirstSeenAt, r.UpdatedAt, r.AssessmentStatus, r.ResultIds, r.PromptIds, r.CheckedSources, r.Explanation, r.AssessedAt)
		if e != nil {
			return nil, e
		}
		o.Observations = observations[o.ID]
		out = append(out, o)
	}
	return out, nil
}

func (s *Store) GetOpportunity(ctx context.Context, accountID, id domain.ID) (OpportunityRecord, error) {
	r, err := s.q(ctx).GetCurrentOpportunity(ctx, storesqlc.GetCurrentOpportunityParams{ID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return OpportunityRecord{}, ErrNotFound
	}
	if err != nil {
		return OpportunityRecord{}, err
	}
	o, err := opportunityFromFields(r.ID, r.BusinessID, r.PracticeKey, r.SubjectKey, r.Rank, r.Presentation, r.UserStatus, r.DismissalReason, r.CompletionBaseline, r.Current, r.Focus, r.FirstSeenAt, r.UpdatedAt, r.AssessmentStatus, r.ResultIds, r.PromptIds, r.CheckedSources, r.Explanation, r.AssessedAt)
	if err != nil {
		return OpportunityRecord{}, err
	}
	o.Observations, err = s.loadOpportunityObservations(ctx, accountID, o.ID)
	return o, err
}

func (s *Store) SetOpportunityStatus(ctx context.Context, accountID, id domain.ID, status OpportunityStatus, reason *string) (OpportunityRecord, error) {
	switch status {
	case StatusOpen, StatusInProgress, StatusCompleted, StatusDismissed:
	default:
		return OpportunityRecord{}, errors.New("invalid opportunity status")
	}
	if status == StatusDismissed {
		if reason == nil {
			return OpportunityRecord{}, errors.New("dismissal reason is required")
		}
	} else {
		reason = nil
	}
	current, err := s.GetOpportunity(ctx, accountID, id)
	if err != nil {
		return OpportunityRecord{}, err
	}
	sameReason := (current.DismissalReason == nil && reason == nil) || (current.DismissalReason != nil && reason != nil && *current.DismissalReason == *reason)
	if current.UserStatus == status && sameReason {
		return current, nil
	}
	var baseline *json.RawMessage
	if status == StatusCompleted {
		encoded, _ := json.Marshal(map[string]any{"result_ids": current.ResultIDs, "prompt_ids": current.PromptIDs, "completed_at": time.Now().UTC()})
		raw := json.RawMessage(encoded)
		baseline = &raw
	}
	eventID, err := domain.NewID()
	if err != nil {
		return OpportunityRecord{}, err
	}
	eventType := map[OpportunityStatus]string{StatusInProgress: "STARTED", StatusCompleted: "COMPLETED", StatusDismissed: "DISMISSED", StatusOpen: "RESTORED"}[status]
	eventPayload, _ := json.Marshal(map[string]any{"reason": reason})

	// The status change and the event that records it go in one transaction, so
	// history can never be missing a transition that happened. The event key is
	// the row's new updated_at, which the same UPDATE set: a replayed insert
	// carries the same key and is absorbed by ON CONFLICT DO NOTHING, while a
	// genuine later transition gets a later timestamp and its own row.
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		row, err := q.SetOpportunityStatus(ctx, storesqlc.SetOpportunityStatusParams{ID: id, AccountID: accountID, UserStatus: string(status), DismissalReason: reason, CompletionBaseline: baseline})
		if err != nil {
			return err
		}
		eventKey := fmt.Sprintf("status:%s:%s", status, row.UpdatedAt.UTC().Format(time.RFC3339Nano))
		return q.InsertOpportunityEvent(ctx, storesqlc.InsertOpportunityEventParams{ID: eventID, OpportunityID: id, AccountID: accountID, EventKey: eventKey, EventType: eventType, Payload: eventPayload})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OpportunityRecord{}, ErrNotFound
	}
	if err != nil {
		return OpportunityRecord{}, err
	}
	return s.GetOpportunity(ctx, accountID, id)
}
