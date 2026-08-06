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
	AssessorKey                      string                 `json:"assessor_key"`
	ModuleVersion                    int                    `json:"module_version"`
	Mode                             visibility.RolloutMode `json:"mode"`
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

func (s *Store) SaveAssessment(ctx context.Context, generationID, accountID, businessID domain.ID, d visibility.AssessmentDraft, mode visibility.RolloutMode) (domain.ID, error) {
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
	stored, err := s.q(ctx).UpsertVisibilityAssessment(ctx, storesqlc.UpsertVisibilityAssessmentParams{ID: id, GenerationID: generationID, AccountID: accountID, BusinessID: businessID, PracticeKey: d.PracticeKey, CriteriaVersion: int32(d.CriteriaVersion), AssessorKey: d.AssessorKey, AssessorVersion: int32(d.AssessorVersion), SubjectKey: d.SubjectKey, Status: string(d.Status), RolloutMode: string(mode), ResultIds: resultIDs, PromptIds: promptIDs, CheckedSources: d.CheckedSources, Confidence: d.Confidence, Explanation: d.Explanation, Reach: int32(d.Reach), Persistence: int32(d.Persistence), EvidenceQuality: int32(d.EvidenceQuality), Actionability: int32(d.Actionability), Effort: int32(d.Effort), PayloadVersion: int32(d.PayloadVersion), Payload: d.Payload})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("save assessment: %w", err)
	}
	return stored, nil
}

func (s *Store) SaveOpportunity(ctx context.Context, accountID, businessID, assessmentID domain.ID, rank int, d visibility.AssessmentDraft, p visibility.Presentation) (domain.ID, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := domain.NewID()
	if err != nil {
		return uuid.Nil, err
	}
	stored, err := s.q(ctx).UpsertOpportunity(ctx, storesqlc.UpsertOpportunityParams{ID: id, AccountID: accountID, BusinessID: businessID, PracticeKey: d.PracticeKey, SubjectKey: d.SubjectKey, CurrentAssessmentID: assessmentID, Rank: int32(rank), Presentation: raw})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("save opportunity: %w", err)
	}
	return stored, nil
}

func (s *Store) RefreshExistingOpportunity(ctx context.Context, accountID, businessID, assessmentID domain.ID, d visibility.AssessmentDraft, p visibility.Presentation) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.q(ctx).RefreshExistingOpportunity(ctx, storesqlc.RefreshExistingOpportunityParams{CurrentAssessmentID: assessmentID, Presentation: raw, BusinessID: businessID, AccountID: accountID, PracticeKey: d.PracticeKey, SubjectKey: d.SubjectKey})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
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
	ID, BusinessID                     domain.ID
	PracticeKey, SubjectKey            string
	Rank                               int
	Presentation                       visibility.Presentation
	UserStatus                         OpportunityStatus
	DismissalReason                    *string
	CompletionBaseline                 json.RawMessage
	FirstSeenAt, UpdatedAt, AssessedAt time.Time
	AssessmentStatus                   visibility.AssessmentStatus
	Confidence                         float64
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

func (s *Store) loadOpportunityObservations(ctx context.Context, accountID, opportunityID domain.ID) ([]OpportunityObservation, error) {
	rows, err := s.q(ctx).ListOpportunityOutcomeEvents(ctx, storesqlc.ListOpportunityOutcomeEventsParams{OpportunityID: opportunityID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	out := make([]OpportunityObservation, 0, len(rows))
	for _, row := range rows {
		var payload struct {
			AssessmentStatus visibility.AssessmentStatus `json:"assessment_status"`
			ResultIDs        []string                    `json:"result_ids"`
			PromptIDs        []string                    `json:"prompt_ids"`
		}
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			return nil, fmt.Errorf("decode outcome observation: %w", err)
		}
		out = append(out, OpportunityObservation{AssessmentStatus: payload.AssessmentStatus, ObservedAt: row.CreatedAt, ResultIDs: payload.ResultIDs, PromptIDs: payload.PromptIDs})
	}
	return out, nil
}

func opportunityFromFields(id, businessID domain.ID, practice, subject string, rank int32, presentation json.RawMessage, userStatus string, dismissal *string, baseline *json.RawMessage, first, updated time.Time, assessment string, confidence float64, resultIDs, promptIDs []uuid.UUID, sources []string, explanation string, assessed time.Time) (OpportunityRecord, error) {
	var p visibility.Presentation
	if err := json.Unmarshal(presentation, &p); err != nil {
		return OpportunityRecord{}, err
	}
	var b json.RawMessage
	if baseline != nil {
		b = *baseline
	}
	return OpportunityRecord{ID: id, BusinessID: businessID, PracticeKey: practice, SubjectKey: subject, Rank: int(rank), Presentation: p, UserStatus: OpportunityStatus(userStatus), DismissalReason: dismissal, CompletionBaseline: b, FirstSeenAt: first, UpdatedAt: updated, AssessmentStatus: visibility.AssessmentStatus(assessment), Confidence: confidence, ResultIDs: resultIDs, PromptIDs: promptIDs, CheckedSources: sources, Explanation: explanation, AssessedAt: assessed}, nil
}

func (s *Store) ListOpportunities(ctx context.Context, accountID, businessID domain.ID) ([]OpportunityRecord, error) {
	if err := businessOwned(ctx, s.q(ctx), accountID, businessID); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListCurrentOpportunities(ctx, storesqlc.ListCurrentOpportunitiesParams{BusinessID: businessID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	out := make([]OpportunityRecord, 0, len(rows))
	for _, r := range rows {
		o, e := opportunityFromFields(r.ID, businessID, r.PracticeKey, r.SubjectKey, r.Rank, r.Presentation, r.UserStatus, r.DismissalReason, r.CompletionBaseline, r.FirstSeenAt, r.UpdatedAt, r.AssessmentStatus, r.Confidence, r.ResultIds, r.PromptIds, r.CheckedSources, r.Explanation, r.AssessedAt)
		if e != nil {
			return nil, e
		}
		o.Observations, e = s.loadOpportunityObservations(ctx, accountID, o.ID)
		if e != nil {
			return nil, e
		}
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
	o, err := opportunityFromFields(r.ID, r.BusinessID, r.PracticeKey, r.SubjectKey, r.Rank, r.Presentation, r.UserStatus, r.DismissalReason, r.CompletionBaseline, r.FirstSeenAt, r.UpdatedAt, r.AssessmentStatus, r.Confidence, r.ResultIds, r.PromptIds, r.CheckedSources, r.Explanation, r.AssessedAt)
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
	_, err = s.q(ctx).SetOpportunityStatus(ctx, storesqlc.SetOpportunityStatusParams{ID: id, AccountID: accountID, UserStatus: string(status), DismissalReason: reason, CompletionBaseline: baseline})
	if errors.Is(err, pgx.ErrNoRows) {
		return OpportunityRecord{}, ErrNotFound
	}
	if err != nil {
		return OpportunityRecord{}, err
	}
	eventType := map[OpportunityStatus]string{StatusInProgress: "STARTED", StatusCompleted: "COMPLETED", StatusDismissed: "DISMISSED", StatusOpen: "RESTORED"}[status]
	eventID, err := domain.NewID()
	if err != nil {
		return OpportunityRecord{}, err
	}
	eventPayload, _ := json.Marshal(map[string]any{"reason": reason})
	eventKey := fmt.Sprintf("status:%s:%d", status, time.Now().UTC().UnixNano())
	if err := s.q(ctx).InsertOpportunityEvent(ctx, storesqlc.InsertOpportunityEventParams{ID: eventID, OpportunityID: id, AccountID: accountID, EventKey: eventKey, EventType: eventType, Payload: eventPayload}); err != nil {
		return OpportunityRecord{}, err
	}
	return s.GetOpportunity(ctx, accountID, id)
}
