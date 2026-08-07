package api

import (
	"context"
	"sort"
	"strings"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/store"
	"opensight/internal/visibility"
)

var _ opensightv1connect.ImproveServiceHandler = (*Server)(nil)

func idsToStrings(ids []domain.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

func practiceModeToProto(mode visibility.PracticeMode) opensightv1.PracticeMode {
	switch mode {
	case visibility.ModeCheck:
		return opensightv1.PracticeMode_PRACTICE_MODE_CHECK
	case visibility.ModeContinuous:
		return opensightv1.PracticeMode_PRACTICE_MODE_CONTINUOUS
	case visibility.ModeTracked:
		return opensightv1.PracticeMode_PRACTICE_MODE_TRACKED
	}
	return opensightv1.PracticeMode_PRACTICE_MODE_UNSPECIFIED
}
func standingToProto(standing visibility.ChecklistStanding) opensightv1.ChecklistStanding {
	switch standing {
	case visibility.StandingGood:
		return opensightv1.ChecklistStanding_CHECKLIST_STANDING_GOOD
	case visibility.StandingImprovable:
		return opensightv1.ChecklistStanding_CHECKLIST_STANDING_IMPROVABLE
	case visibility.StandingNeedsAttention:
		return opensightv1.ChecklistStanding_CHECKLIST_STANDING_NEEDS_ATTENTION
	case visibility.StandingTracking:
		return opensightv1.ChecklistStanding_CHECKLIST_STANDING_TRACKING
	case visibility.StandingCouldNotVerify:
		return opensightv1.ChecklistStanding_CHECKLIST_STANDING_COULD_NOT_VERIFY
	case visibility.StandingNotAssessed:
		return opensightv1.ChecklistStanding_CHECKLIST_STANDING_NOT_ASSESSED
	case visibility.StandingNotApplicable:
		return opensightv1.ChecklistStanding_CHECKLIST_STANDING_NOT_APPLICABLE
	case visibility.StandingNoLongerTracked:
		return opensightv1.ChecklistStanding_CHECKLIST_STANDING_NO_LONGER_TRACKED
	}
	return opensightv1.ChecklistStanding_CHECKLIST_STANDING_UNSPECIFIED
}
func actionStatusToProto(status store.ActionStatus) opensightv1.ActionStatus {
	switch status {
	case store.ActionOpen:
		return opensightv1.ActionStatus_ACTION_STATUS_OPEN
	case store.ActionInProgress:
		return opensightv1.ActionStatus_ACTION_STATUS_IN_PROGRESS
	case store.ActionCompleted:
		return opensightv1.ActionStatus_ACTION_STATUS_COMPLETED
	case store.ActionDismissed:
		return opensightv1.ActionStatus_ACTION_STATUS_DISMISSED
	case store.ActionRetired:
		return opensightv1.ActionStatus_ACTION_STATUS_RETIRED
	case store.ActionSuperseded:
		return opensightv1.ActionStatus_ACTION_STATUS_SUPERSEDED
	}
	return opensightv1.ActionStatus_ACTION_STATUS_UNSPECIFIED
}
func actionStatusFromProto(status opensightv1.ActionStatus) store.ActionStatus {
	switch status {
	case opensightv1.ActionStatus_ACTION_STATUS_OPEN:
		return store.ActionOpen
	case opensightv1.ActionStatus_ACTION_STATUS_IN_PROGRESS:
		return store.ActionInProgress
	case opensightv1.ActionStatus_ACTION_STATUS_COMPLETED:
		return store.ActionCompleted
	case opensightv1.ActionStatus_ACTION_STATUS_DISMISSED:
		return store.ActionDismissed
	}
	return ""
}
func dismissalToProto(reason *string) opensightv1.DismissalReason {
	if reason == nil {
		return opensightv1.DismissalReason_DISMISSAL_REASON_UNSPECIFIED
	}
	return map[string]opensightv1.DismissalReason{"NOT_RELEVANT": opensightv1.DismissalReason_DISMISSAL_REASON_NOT_RELEVANT, "ALREADY_DONE": opensightv1.DismissalReason_DISMISSAL_REASON_ALREADY_DONE, "NOT_ACTIONABLE": opensightv1.DismissalReason_DISMISSAL_REASON_NOT_ACTIONABLE, "TOO_MUCH_EFFORT": opensightv1.DismissalReason_DISMISSAL_REASON_TOO_MUCH_EFFORT, "OTHER": opensightv1.DismissalReason_DISMISSAL_REASON_OTHER}[*reason]
}
func dismissalFromProto(reason opensightv1.DismissalReason) *string {
	value := map[opensightv1.DismissalReason]string{opensightv1.DismissalReason_DISMISSAL_REASON_NOT_RELEVANT: "NOT_RELEVANT", opensightv1.DismissalReason_DISMISSAL_REASON_ALREADY_DONE: "ALREADY_DONE", opensightv1.DismissalReason_DISMISSAL_REASON_NOT_ACTIONABLE: "NOT_ACTIONABLE", opensightv1.DismissalReason_DISMISSAL_REASON_TOO_MUCH_EFFORT: "TOO_MUCH_EFFORT", opensightv1.DismissalReason_DISMISSAL_REASON_OTHER: "OTHER"}[reason]
	if value == "" {
		return nil
	}
	return &value
}

func freshnessToProto(f store.Freshness) *opensightv1.AssessmentFreshness {
	out := &opensightv1.AssessmentFreshness{Available: f.Available, Partial: f.Partial, Stale: f.Stale}
	if f.AssessedAt != nil {
		out.AssessedAt = timestamppb.New(*f.AssessedAt)
	}
	return out
}
func actionBlockTypeToProto(t visibility.BlockType) opensightv1.ActionBlockType {
	switch t {
	case visibility.BlockText:
		return opensightv1.ActionBlockType_ACTION_BLOCK_TYPE_TEXT
	case visibility.BlockMetric:
		return opensightv1.ActionBlockType_ACTION_BLOCK_TYPE_METRIC
	case visibility.BlockLink:
		return opensightv1.ActionBlockType_ACTION_BLOCK_TYPE_LINK
	case visibility.BlockQuestionList:
		return opensightv1.ActionBlockType_ACTION_BLOCK_TYPE_QUESTION_LIST
	case visibility.BlockEvidenceList:
		return opensightv1.ActionBlockType_ACTION_BLOCK_TYPE_EVIDENCE_LIST
	case visibility.BlockNotice:
		return opensightv1.ActionBlockType_ACTION_BLOCK_TYPE_NOTICE
	}
	return opensightv1.ActionBlockType_ACTION_BLOCK_TYPE_UNSPECIFIED
}
func actionToProto(action store.ActionRecord) *opensightv1.ImprovementAction {
	out := &opensightv1.ImprovementAction{Id: action.ID.String(), PracticeKey: action.PracticeKey, SubjectKey: action.SubjectKey, Cycle: int32(action.Cycle), RecommendationKey: action.RecommendationKey, Title: action.Presentation.Title, Summary: action.Presentation.Summary, Effort: action.Presentation.Effort, Status: actionStatusToProto(action.Status), DismissalReason: dismissalToProto(action.DismissalReason), ResultIds: idsToStrings(action.ResultIDs), PromptIds: idsToStrings(action.PromptIDs), CheckedSources: action.CheckedSources, Fresh: action.Fresh}
	if def, ok := visibility.Practice(action.PracticeKey); ok {
		out.Standing = standingToProto(visibility.Standing(def, action.AssessmentStatus))
	}
	if action.AssessedAt != nil {
		out.AssessedAt = timestamppb.New(*action.AssessedAt)
	}
	for _, b := range action.Presentation.Blocks {
		out.Blocks = append(out.Blocks, &opensightv1.ActionBlock{Type: actionBlockTypeToProto(b.Type), Title: b.Title, Text: b.Text, Value: b.Value, Url: b.URL, Items: b.Items, ResultIds: b.ResultIDs})
	}
	return out
}

func (s *Server) ListActions(ctx context.Context, req *connect.Request[opensightv1.ListActionsRequest]) (*connect.Response[opensightv1.ListActionsResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "list actions")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}
	rows, err := s.store.ListActions(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("list actions", err)
	}
	_, freshness, err := s.store.PublishedAssessments(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("load assessment freshness", err)
	}
	resp := &opensightv1.ListActionsResponse{Freshness: freshnessToProto(freshness)}
	for _, row := range rows {
		if row.Status != store.ActionOpen && row.Status != store.ActionInProgress {
			continue
		}
		item := actionToProto(row)
		if len(resp.FocusActions) < 3 {
			resp.FocusActions = append(resp.FocusActions, item)
		} else {
			resp.AdditionalActions = append(resp.AdditionalActions, item)
		}
	}
	if len(resp.FocusActions) == 0 {
		switch {
		case !freshness.Available:
			resp.EmptyReason = opensightv1.ActionsEmptyReason_ACTIONS_EMPTY_REASON_INSUFFICIENT_CAPABILITY
		case freshness.Partial || freshness.Stale:
			resp.EmptyReason = opensightv1.ActionsEmptyReason_ACTIONS_EMPTY_REASON_INCOMPLETE_CHECKS
		default:
			resp.EmptyReason = opensightv1.ActionsEmptyReason_ACTIONS_EMPTY_REASON_HEALTHY
		}
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) GetAction(ctx context.Context, req *connect.Request[opensightv1.GetActionRequest]) (*connect.Response[opensightv1.GetActionResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get action")
	if cerr != nil {
		return nil, cerr
	}
	id, cerr := rpcID("action_id", req.Msg.ActionId)
	if cerr != nil {
		return nil, cerr
	}
	row, err := s.store.GetAction(ctx, su.AccountID, id)
	if err != nil {
		return nil, s.rpcError("get action", err)
	}
	out := actionToProto(row)
	cycles, err := s.store.ListActionCycles(ctx, su.AccountID, row)
	if err != nil {
		return nil, s.rpcError("list action cycles", err)
	}
	for _, cycle := range cycles {
		item := &opensightv1.ActionCycle{Id: cycle.ID.String(), Cycle: int32(cycle.Cycle), Status: actionStatusToProto(cycle.Status), FirstSeenAt: timestamppb.New(cycle.FirstSeenAt), UpdatedAt: timestamppb.New(cycle.UpdatedAt)}
		if cycle.CompletedAt != nil {
			item.CompletedAt = timestamppb.New(*cycle.CompletedAt)
		}
		out.Cycles = append(out.Cycles, item)
	}
	return connect.NewResponse(&opensightv1.GetActionResponse{Action: out}), nil
}
func (s *Server) SetActionStatus(ctx context.Context, req *connect.Request[opensightv1.SetActionStatusRequest]) (*connect.Response[opensightv1.SetActionStatusResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "set action status")
	if cerr != nil {
		return nil, cerr
	}
	id, cerr := rpcID("action_id", req.Msg.ActionId)
	if cerr != nil {
		return nil, cerr
	}
	status := actionStatusFromProto(req.Msg.Status)
	if status == "" {
		return nil, rpcInvalidArgument("status is invalid")
	}
	row, err := s.store.SetActionStatus(ctx, su.AccountID, id, status, dismissalFromProto(req.Msg.DismissalReason))
	if err != nil {
		return nil, s.rpcError("set action status", err)
	}
	return connect.NewResponse(&opensightv1.SetActionStatusResponse{Action: actionToProto(row)}), nil
}

func worstStanding(values []visibility.ChecklistStanding) visibility.ChecklistStanding {
	rank := map[visibility.ChecklistStanding]int{visibility.StandingNeedsAttention: 8, visibility.StandingImprovable: 7, visibility.StandingCouldNotVerify: 6, visibility.StandingNotAssessed: 5, visibility.StandingNoLongerTracked: 4, visibility.StandingTracking: 3, visibility.StandingNotApplicable: 2, visibility.StandingGood: 1}
	best := visibility.StandingNotAssessed
	for _, v := range values {
		if rank[v] > rank[best] {
			best = v
		}
	}
	return best
}
func (s *Server) GetChecklist(ctx context.Context, req *connect.Request[opensightv1.GetChecklistRequest]) (*connect.Response[opensightv1.GetChecklistResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get checklist")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}
	assessments, freshness, err := s.store.PublishedAssessments(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("get checklist assessments", err)
	}
	actions, err := s.store.ListActions(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("get checklist actions", err)
	}
	assessed := map[string]store.PublishedAssessment{}
	subjects := map[string]map[string]bool{}
	history := map[string][]store.ActionRecord{}
	for _, a := range assessments {
		key := a.PracticeKey + "\x00" + a.SubjectKey
		assessed[key] = a
		if subjects[a.PracticeKey] == nil {
			subjects[a.PracticeKey] = map[string]bool{}
		}
		subjects[a.PracticeKey][a.SubjectKey] = true
	}
	for _, a := range actions {
		key := a.PracticeKey + "\x00" + a.SubjectKey
		history[key] = append(history[key], a)
		if subjects[a.PracticeKey] == nil {
			subjects[a.PracticeKey] = map[string]bool{}
		}
		subjects[a.PracticeKey][a.SubjectKey] = true
	}
	resp := &opensightv1.GetChecklistResponse{Freshness: freshnessToProto(freshness)}
	sectionIndex := map[string]*opensightv1.ChecklistSection{}
	counts := map[visibility.ChecklistStanding]int{}
	for _, def := range visibility.Catalog() {
		section := sectionIndex[def.Section]
		if section == nil {
			section = &opensightv1.ChecklistSection{Title: def.Section}
			sectionIndex[def.Section] = section
			resp.Sections = append(resp.Sections, section)
		}
		practice := &opensightv1.ChecklistPractice{Key: def.Key, Title: def.Title, Description: def.Description, Why: def.Why, Mode: practiceModeToProto(def.Mode), References: def.References}
		keys := []string{}
		for subject := range subjects[def.Key] {
			keys = append(keys, subject)
		}
		if def.SubjectScope == visibility.ScopeBusiness {
			keys = []string{visibility.BusinessSubjectKey}
		} else if len(keys) == 0 {
			keys = []string{""}
		}
		sort.Strings(keys)
		standings := []visibility.ChecklistStanding{}
		for _, subject := range keys {
			identity := def.Key + "\x00" + subject
			assessment, ok := assessed[identity]
			standing := visibility.StandingNotAssessed
			explanation := "OpenSight does not currently have an assessment for this practice."
			stale := false
			var checkedAt *timestamppb.Timestamp
			if ok {
				standing = visibility.Standing(def, assessment.Status)
				explanation = assessment.Explanation
				stale = !assessment.Fresh
				checkedAt = timestamppb.New(assessment.AssessedAt)
			} else if subject != "" && len(history[identity]) > 0 {
				standing = visibility.StandingNoLongerTracked
				explanation = "This subject is no longer present in the latest successful check."
			}
			entry := &opensightv1.ChecklistSubject{SubjectKey: subject, Label: subject, Standing: standingToProto(standing), StandingLabel: visibility.ModeLabel(def.Mode, standing), Explanation: explanation, LastSuccessfulCheck: checkedAt, Stale: stale}
			if ok {
				entry.ResultIds = idsToStrings(assessment.ResultIDs)
				entry.PromptIds = idsToStrings(assessment.PromptIDs)
				entry.CheckedSources = assessment.CheckedSources
			}
			for _, a := range history[identity] {
				entry.ActionHistory = append(entry.ActionHistory, &opensightv1.ChecklistActionHistory{ActionId: a.ID.String(), Cycle: int32(a.Cycle), Status: actionStatusToProto(a.Status), UpdatedAt: timestamppb.New(a.UpdatedAt)})
				if entry.CurrentAction == nil && (a.Status == store.ActionOpen || a.Status == store.ActionInProgress) {
					entry.CurrentAction = actionToProto(a)
				}
			}
			practice.Subjects = append(practice.Subjects, entry)
			standings = append(standings, standing)
			counts[standing]++
		}
		practiceStanding := worstStanding(standings)
		practice.Standing = standingToProto(practiceStanding)
		practice.StandingLabel = visibility.ModeLabel(def.Mode, practiceStanding)
		section.Practices = append(section.Practices, practice)
	}
	order := []visibility.ChecklistStanding{visibility.StandingGood, visibility.StandingImprovable, visibility.StandingNeedsAttention, visibility.StandingTracking, visibility.StandingCouldNotVerify, visibility.StandingNotAssessed, visibility.StandingNotApplicable, visibility.StandingNoLongerTracked}
	for _, standing := range order {
		if count := counts[standing]; count > 0 {
			resp.Counts = append(resp.Counts, &opensightv1.StandingCount{Standing: standingToProto(standing), Count: int32(count)})
		}
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) ListActivity(ctx context.Context, req *connect.Request[opensightv1.ListActivityRequest]) (*connect.Response[opensightv1.ListActivityResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "list activity")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}
	limit, offset := rpcPaging(req.Msg.Limit, req.Msg.Offset, 20, 100)
	rows, total, err := s.store.ListActivity(ctx, su.AccountID, businessID, limit, offset)
	if err != nil {
		return nil, s.rpcError("list activity", err)
	}
	resp := &opensightv1.ListActivityResponse{Total: int32(total), Limit: int32(limit), Offset: int32(offset)}
	for _, row := range rows {
		resp.Events = append(resp.Events, &opensightv1.ActivityEvent{Id: row.ID.String(), ActionId: row.ActionID.String(), PracticeKey: row.PracticeKey, SubjectKey: row.SubjectKey, Title: row.Title, Cycle: int32(row.Cycle), EventType: strings.ToLower(row.EventType), CreatedAt: timestamppb.New(row.CreatedAt)})
	}
	return connect.NewResponse(resp), nil
}
