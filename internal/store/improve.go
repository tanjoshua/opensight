package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
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

type ModuleOutcome struct {
	AssessorKey string
	Status      string
	Error       string
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
	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, fmt.Errorf("invalid evidence id %q: %w", value, err)
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
	checkedSources := d.CheckedSources
	if checkedSources == nil {
		checkedSources = []string{}
	}
	stored, err := s.q(ctx).UpsertVisibilityAssessment(ctx, storesqlc.UpsertVisibilityAssessmentParams{ID: id, GenerationID: generationID, AccountID: accountID, BusinessID: businessID, PracticeKey: d.PracticeKey, CriteriaVersion: int32(d.CriteriaVersion), AssessorKey: d.AssessorKey, AssessorVersion: int32(d.AssessorVersion), SubjectKey: d.SubjectKey, Status: string(d.Status), ResultIds: resultIDs, PromptIds: promptIDs, CheckedSources: checkedSources, Explanation: d.Explanation, Reach: int32(d.Reach), Persistence: int32(d.Persistence), EvidenceQuality: int32(d.EvidenceQuality), Actionability: int32(d.Actionability), Effort: int32(d.Effort), PayloadVersion: int32(d.PayloadVersion), Payload: d.Payload})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return stored, err
}

type CompiledAction struct {
	AssessmentID domain.ID
	visibility.CompiledAssessment
}

func recommendationKey(d visibility.AssessmentDraft) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00", d.PracticeKey, d.SubjectKey, d.CriteriaVersion)
	_, _ = h.Write(d.Payload)
	return hex.EncodeToString(h.Sum(nil))[:24]
}

func actionIdentity(practice, subject string) string { return practice + "\x00" + subject }

func insertEvent(ctx context.Context, q *storesqlc.Queries, accountID, actionID domain.ID, key, eventType string, payload json.RawMessage) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return q.InsertImprovementActionEvent(ctx, storesqlc.InsertImprovementActionEventParams{ID: id, ActionID: actionID, AccountID: accountID, EventKey: key, EventType: eventType, Payload: payload})
}

// PublishAssessmentGeneration is the only publication boundary. Successful
// practice scopes, their actions, module outcomes, and the generation terminal
// state become visible in one transaction. Failed and skipped scopes retain
// their prior published assessment and active action unchanged.
func (s *Store) PublishAssessmentGeneration(ctx context.Context, accountID, businessID, generationID domain.ID, outcomes []ModuleOutcome, items []CompiledAction) error {
	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		generationStatus, err := q.LockAssessmentGeneration(ctx, storesqlc.LockAssessmentGenerationParams{ID: generationID, AccountID: accountID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if generationStatus == "READY" || generationStatus == "PARTIAL" {
			return nil
		}
		if generationStatus != "RUNNING" {
			return errors.New("assessment generation is not publishable")
		}
		succeeded := map[string]bool{}
		practiceKeys := []string{}
		for _, outcome := range outcomes {
			var msg *string
			if outcome.Error != "" {
				msg = &outcome.Error
			}
			if err := q.UpsertModuleOutcome(ctx, storesqlc.UpsertModuleOutcomeParams{GenerationID: generationID, AccountID: accountID, AssessorKey: outcome.AssessorKey, Status: outcome.Status, Error: msg}); err != nil {
				return err
			}
			if outcome.Status == "SUCCEEDED" {
				succeeded[outcome.AssessorKey] = true
			}
		}
		for _, def := range visibility.Catalog() {
			if succeeded[def.AssessorKey] {
				practiceKeys = append(practiceKeys, def.Key)
			}
		}
		sort.Strings(practiceKeys)
		priorRows, err := q.ListPublishedForPractices(ctx, storesqlc.ListPublishedForPracticesParams{BusinessID: businessID, AccountID: accountID, PracticeKeys: practiceKeys})
		if err != nil {
			return err
		}
		prior := map[string]storesqlc.VisibilityAssessment{}
		for _, row := range priorRows {
			prior[actionIdentity(row.PracticeKey, row.SubjectKey)] = row
		}
		activeRows, err := q.ListActiveActionsForPractices(ctx, storesqlc.ListActiveActionsForPracticesParams{BusinessID: businessID, AccountID: accountID, PracticeKeys: practiceKeys})
		if err != nil {
			return err
		}
		active := map[string]storesqlc.ImprovementAction{}
		for _, row := range activeRows {
			active[actionIdentity(row.PracticeKey, row.SubjectKey)] = row
		}
		if err := q.UnpublishPractices(ctx, storesqlc.UnpublishPracticesParams{BusinessID: businessID, AccountID: accountID, PracticeKeys: practiceKeys}); err != nil {
			return err
		}
		if err := q.PublishGenerationPractices(ctx, storesqlc.PublishGenerationPracticesParams{GenerationID: generationID, AccountID: accountID, PracticeKeys: practiceKeys}); err != nil {
			return err
		}

		applicable := map[string]bool{}
		for rank, item := range items {
			if !succeeded[item.Draft.AssessorKey] {
				continue
			}
			identity := actionIdentity(item.Draft.PracticeKey, item.Draft.SubjectKey)
			applicable[identity] = true
			key := recommendationKey(item.Draft)
			presentation, err := json.Marshal(item.Presentation)
			if err != nil {
				return err
			}
			assessmentID := item.AssessmentID
			if current, ok := active[identity]; ok {
				if current.RecommendationKey == key {
					if err := q.UpdateActiveImprovementAction(ctx, storesqlc.UpdateActiveImprovementActionParams{CurrentAssessmentID: &assessmentID, Rank: int32(rank + 1), Presentation: presentation, ID: current.ID, AccountID: accountID}); err != nil {
						return err
					}
					continue
				}
				if err := q.EndImprovementAction(ctx, storesqlc.EndImprovementActionParams{Status: "SUPERSEDED", CurrentAssessmentID: &assessmentID, ID: current.ID, AccountID: accountID}); err != nil {
					return err
				}
				if err := insertEvent(ctx, q, accountID, current.ID, "superseded:"+generationID.String(), "SUPERSEDED", nil); err != nil {
					return err
				}
			}
			cycles, err := q.ListActionCycles(ctx, storesqlc.ListActionCyclesParams{BusinessID: businessID, AccountID: accountID, PracticeKey: item.Draft.PracticeKey, SubjectKey: item.Draft.SubjectKey})
			if err != nil {
				return err
			}
			cycle, eventType, create := int32(1), "CREATED", true
			if len(cycles) > 0 {
				last := cycles[0]
				cycle = last.Cycle + 1
				eventType = "RECURRED"
				switch last.Status {
				case "DISMISSED":
					create = last.RecommendationKey != key
				case "COMPLETED":
					old, hadPrior := prior[identity]
					create = last.RecommendationKey != key || hadPrior && old.Status == string(visibility.StatusMet)
				}
				if create && last.RecommendationKey != key {
					if err := insertEvent(ctx, q, accountID, last.ID, "superseded:"+generationID.String(), "SUPERSEDED", nil); err != nil {
						return err
					}
				}
			}
			if !create {
				continue
			}
			id, err := domain.NewID()
			if err != nil {
				return err
			}
			created, err := q.InsertImprovementAction(ctx, storesqlc.InsertImprovementActionParams{ID: id, AccountID: accountID, BusinessID: businessID, PracticeKey: item.Draft.PracticeKey, SubjectKey: item.Draft.SubjectKey, Cycle: cycle, RecommendationKey: key, CurrentAssessmentID: &assessmentID, Rank: int32(rank + 1), Presentation: presentation})
			if err != nil {
				return err
			}
			if err := insertEvent(ctx, q, accountID, created.ID, strings.ToLower(eventType)+":"+generationID.String(), eventType, nil); err != nil {
				return err
			}
		}
		for identity, current := range active {
			if applicable[identity] {
				continue
			}
			if err := q.EndImprovementAction(ctx, storesqlc.EndImprovementActionParams{Status: "RETIRED", CurrentAssessmentID: current.CurrentAssessmentID, ID: current.ID, AccountID: accountID}); err != nil {
				return err
			}
			if err := insertEvent(ctx, q, accountID, current.ID, "retired:"+generationID.String(), "RETIRED", nil); err != nil {
				return err
			}
		}
		status := "READY"
		for _, outcome := range outcomes {
			if outcome.Status != "SUCCEEDED" {
				status = "PARTIAL"
				break
			}
		}
		return q.FinishAssessmentGeneration(ctx, storesqlc.FinishAssessmentGenerationParams{Status: status, ID: generationID, AccountID: accountID})
	})
}

func (s *Store) FailAssessmentGeneration(ctx context.Context, generationID, accountID domain.ID, outcomes []ModuleOutcome, failure error) error {
	msg := "assessment compilation failed"
	if failure != nil {
		msg = failure.Error()
	}
	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		generationStatus, err := q.LockAssessmentGeneration(ctx, storesqlc.LockAssessmentGenerationParams{ID: generationID, AccountID: accountID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if generationStatus != "RUNNING" {
			return nil
		}
		for _, outcome := range outcomes {
			var outcomeError *string
			if outcome.Error != "" {
				outcomeError = &outcome.Error
			}
			if err := q.UpsertModuleOutcome(ctx, storesqlc.UpsertModuleOutcomeParams{GenerationID: generationID, AccountID: accountID, AssessorKey: outcome.AssessorKey, Status: outcome.Status, Error: outcomeError}); err != nil {
				return err
			}
		}
		return q.FailAssessmentGeneration(ctx, storesqlc.FailAssessmentGenerationParams{Error: &msg, ID: generationID, AccountID: accountID})
	})
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
		snapshot.Runs[i].Results = append(snapshot.Runs[i].Results, visibility.SnapshotResult{ResultID: row.ResultID.String(), PromptID: row.PromptID.String(), Prompt: row.Prompt, ResponseText: text, Mentioned: row.Mentioned, Competitors: row.Competitors, CitationDomains: row.CitationDomains, CitationURLs: row.CitationUrls})
	}
	return snapshot, nil
}

type ActionStatus string

const (
	ActionOpen       ActionStatus = "OPEN"
	ActionInProgress ActionStatus = "IN_PROGRESS"
	ActionCompleted  ActionStatus = "COMPLETED"
	ActionDismissed  ActionStatus = "DISMISSED"
	ActionRetired    ActionStatus = "RETIRED"
	ActionSuperseded ActionStatus = "SUPERSEDED"
)

type ActionRecord struct {
	ID, BusinessID                             domain.ID
	PracticeKey, SubjectKey, RecommendationKey string
	Cycle, Rank                                int
	Presentation                               visibility.Presentation
	Status                                     ActionStatus
	DismissalReason                            *string
	CompletionBaseline                         json.RawMessage
	StartedAt, CompletedAt                     *time.Time
	FirstSeenAt, UpdatedAt                     time.Time
	AssessmentStatus                           visibility.AssessmentStatus
	ResultIDs, PromptIDs                       []domain.ID
	CheckedSources                             []string
	Explanation                                string
	AssessedAt                                 *time.Time
	Fresh                                      bool
}

func actionRecord(row storesqlc.GetBusinessActionRow) (ActionRecord, error) {
	var presentation visibility.Presentation
	if err := json.Unmarshal(row.Presentation, &presentation); err != nil {
		return ActionRecord{}, err
	}
	record := ActionRecord{ID: row.ID, BusinessID: row.BusinessID, PracticeKey: row.PracticeKey, SubjectKey: row.SubjectKey, RecommendationKey: row.RecommendationKey, Cycle: int(row.Cycle), Rank: int(row.Rank), Presentation: presentation, Status: ActionStatus(row.Status), DismissalReason: row.DismissalReason, StartedAt: row.StartedAt, CompletedAt: row.CompletedAt, FirstSeenAt: row.FirstSeenAt, UpdatedAt: row.UpdatedAt, ResultIDs: row.ResultIds, PromptIDs: row.PromptIds, CheckedSources: row.CheckedSources, AssessedAt: row.AssessedAt, Fresh: row.Fresh}
	if row.CompletionBaseline != nil {
		record.CompletionBaseline = *row.CompletionBaseline
	}
	if row.AssessmentStatus != nil {
		record.AssessmentStatus = visibility.AssessmentStatus(*row.AssessmentStatus)
	}
	if row.Explanation != nil {
		record.Explanation = *row.Explanation
	}
	return record, nil
}

func (s *Store) ListActions(ctx context.Context, accountID, businessID domain.ID) ([]ActionRecord, error) {
	if err := businessOwned(ctx, s.q(ctx), accountID, businessID); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListBusinessActions(ctx, storesqlc.ListBusinessActionsParams{BusinessID: businessID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	out := []ActionRecord{}
	for _, row := range rows {
		converted, err := actionRecord(storesqlc.GetBusinessActionRow(row))
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}
func (s *Store) GetAction(ctx context.Context, accountID, id domain.ID) (ActionRecord, error) {
	row, err := s.q(ctx).GetBusinessAction(ctx, storesqlc.GetBusinessActionParams{ID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ActionRecord{}, ErrNotFound
	}
	if err != nil {
		return ActionRecord{}, err
	}
	return actionRecord(row)
}

type ActionCycle struct {
	ID                     domain.ID
	Cycle                  int
	Status                 ActionStatus
	FirstSeenAt, UpdatedAt time.Time
	CompletedAt            *time.Time
}

func (s *Store) ListActionCycles(ctx context.Context, accountID domain.ID, action ActionRecord) ([]ActionCycle, error) {
	rows, err := s.q(ctx).ListActionCycles(ctx, storesqlc.ListActionCyclesParams{BusinessID: action.BusinessID, AccountID: accountID, PracticeKey: action.PracticeKey, SubjectKey: action.SubjectKey})
	if err != nil {
		return nil, err
	}
	out := make([]ActionCycle, 0, len(rows))
	for _, r := range rows {
		out = append(out, ActionCycle{ID: r.ID, Cycle: int(r.Cycle), Status: ActionStatus(r.Status), FirstSeenAt: r.FirstSeenAt, UpdatedAt: r.UpdatedAt, CompletedAt: r.CompletedAt})
	}
	return out, nil
}

func (s *Store) SetActionStatus(ctx context.Context, accountID, id domain.ID, status ActionStatus, reason *string) (ActionRecord, error) {
	current, err := s.GetAction(ctx, accountID, id)
	if err != nil {
		return ActionRecord{}, err
	}
	allowed := false
	switch status {
	case ActionInProgress:
		allowed = current.Status == ActionOpen
	case ActionCompleted:
		allowed = current.Status == ActionInProgress
	case ActionDismissed:
		allowed = (current.Status == ActionOpen || current.Status == ActionInProgress) && reason != nil
	case ActionOpen:
		allowed = current.Status == ActionDismissed
	}
	if !allowed {
		return ActionRecord{}, ErrInvalidActionTransition
	}
	cycles, err := s.ListActionCycles(ctx, accountID, current)
	if err != nil {
		return ActionRecord{}, err
	}
	if len(cycles) == 0 || cycles[0].ID != current.ID {
		return ActionRecord{}, ErrInvalidActionTransition
	}
	if status != ActionDismissed {
		reason = nil
	}
	var baseline *json.RawMessage
	if status == ActionCompleted {
		value, err := s.completionBaseline(ctx, accountID, current)
		if err != nil {
			return ActionRecord{}, err
		}
		raw, _ := json.Marshal(value)
		encoded := json.RawMessage(raw)
		baseline = &encoded
	}
	eventType := map[ActionStatus]string{ActionInProgress: "STARTED", ActionCompleted: "COMPLETED", ActionDismissed: "DISMISSED", ActionOpen: "RESTORED"}[status]
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		row, err := q.SetImprovementActionStatus(ctx, storesqlc.SetImprovementActionStatusParams{Status: string(status), PreviousStatus: string(current.Status), DismissalReason: reason, CompletionBaseline: baseline, ID: id, AccountID: accountID})
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"dismissal_reason": reason})
		return insertEvent(ctx, q, accountID, id, strings.ToLower(eventType)+":"+row.UpdatedAt.UTC().Format(time.RFC3339Nano), eventType, payload)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ActionRecord{}, ErrNotFound
	}
	if err != nil {
		return ActionRecord{}, err
	}
	return s.GetAction(ctx, accountID, id)
}
func (s *Store) completionBaseline(ctx context.Context, accountID domain.ID, action ActionRecord) (map[string]any, error) {
	baseline := map[string]any{"completed_at": time.Now().UTC()}
	if len(action.PromptIDs) > 0 || len(action.ResultIDs) > 0 {
		rows, err := s.q(ctx).LoadCompletionQuestionEvidence(ctx, storesqlc.LoadCompletionQuestionEvidenceParams{BusinessID: action.BusinessID, AccountID: accountID, ResultIds: action.ResultIDs, PromptIds: action.PromptIDs})
		if err != nil {
			return nil, err
		}
		questions := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			questions = append(questions, map[string]any{"prompt_id": row.PromptID.String(), "prompt": row.Prompt, "result_id": row.ResultID.String(), "mentioned": row.Mentioned})
		}
		baseline["questions"] = questions
		return baseline, nil
	}
	totals, err := s.q(ctx).LoadCompletionBusinessTotals(ctx, storesqlc.LoadCompletionBusinessTotalsParams{BusinessID: action.BusinessID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	baseline["business_totals"] = map[string]int32{"mentioned": totals.Mentioned, "analyzed": totals.Analyzed}
	return baseline, nil
}

type PublishedAssessment struct {
	ID, GenerationID        domain.ID
	PracticeKey, SubjectKey string
	CriteriaVersion         int
	Status                  visibility.AssessmentStatus
	ResultIDs, PromptIDs    []domain.ID
	CheckedSources          []string
	Explanation             string
	Payload                 json.RawMessage
	AssessedAt              time.Time
	Fresh                   bool
}
type Freshness struct {
	Available, Partial, Stale bool
	AssessedAt                *time.Time
}

func (s *Store) PublishedAssessments(ctx context.Context, accountID, businessID domain.ID) ([]PublishedAssessment, Freshness, error) {
	if err := businessOwned(ctx, s.q(ctx), accountID, businessID); err != nil {
		return nil, Freshness{}, err
	}
	generation, err := s.q(ctx).GetLatestPublishedGeneration(ctx, storesqlc.GetLatestPublishedGenerationParams{BusinessID: businessID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, Freshness{}, nil
	}
	if err != nil {
		return nil, Freshness{}, err
	}
	rows, err := s.q(ctx).ListPublishedAssessments(ctx, storesqlc.ListPublishedAssessmentsParams{BusinessID: businessID, AccountID: accountID})
	if err != nil {
		return nil, Freshness{}, err
	}
	freshness := Freshness{Available: true, Partial: generation.Status == "PARTIAL", AssessedAt: generation.CompletedAt}
	out := make([]PublishedAssessment, 0, len(rows))
	for _, r := range rows {
		fresh := r.GenerationID == generation.ID
		if !fresh {
			freshness.Stale = true
		}
		out = append(out, PublishedAssessment{ID: r.ID, GenerationID: r.GenerationID, PracticeKey: r.PracticeKey, SubjectKey: r.SubjectKey, CriteriaVersion: int(r.CriteriaVersion), Status: visibility.AssessmentStatus(r.Status), ResultIDs: r.ResultIds, PromptIDs: r.PromptIds, CheckedSources: r.CheckedSources, Explanation: r.Explanation, Payload: r.Payload, AssessedAt: r.AssessedAt, Fresh: fresh})
	}
	return out, freshness, nil
}

type ActivityEvent struct {
	ID, ActionID                              domain.ID
	PracticeKey, SubjectKey, Title, EventType string
	Cycle                                     int
	CreatedAt                                 time.Time
}

func (s *Store) ListActivity(ctx context.Context, accountID, businessID domain.ID, limit, offset int) ([]ActivityEvent, int, error) {
	if err := businessOwned(ctx, s.q(ctx), accountID, businessID); err != nil {
		return nil, 0, err
	}
	rows, err := s.q(ctx).ListActionEvents(ctx, storesqlc.ListActionEventsParams{BusinessID: businessID, AccountID: accountID, PageLimit: int32(limit), PageOffset: int32(offset)})
	if err != nil {
		return nil, 0, err
	}
	count, err := s.q(ctx).CountActionEvents(ctx, storesqlc.CountActionEventsParams{BusinessID: businessID, AccountID: accountID})
	if err != nil {
		return nil, 0, err
	}
	out := make([]ActivityEvent, 0, len(rows))
	for _, r := range rows {
		var p visibility.Presentation
		_ = json.Unmarshal(r.Presentation, &p)
		out = append(out, ActivityEvent{ID: r.ID, ActionID: r.ActionID, PracticeKey: r.PracticeKey, SubjectKey: r.SubjectKey, Title: p.Title, EventType: r.EventType, Cycle: int(r.Cycle), CreatedAt: r.CreatedAt})
	}
	return out, int(count), nil
}
