package api

import (
	"context"

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

func checkOutcomeToProto(outcome visibility.CheckOutcome) opensightv1.CheckOutcome {
	switch outcome {
	case visibility.CheckPass:
		return opensightv1.CheckOutcome_CHECK_OUTCOME_PASS
	case visibility.CheckFail:
		return opensightv1.CheckOutcome_CHECK_OUTCOME_FAIL
	case visibility.CheckCouldNotVerify:
		return opensightv1.CheckOutcome_CHECK_OUTCOME_COULD_NOT_VERIFY
	case visibility.CheckNotApplicable:
		return opensightv1.CheckOutcome_CHECK_OUTCOME_NOT_APPLICABLE
	default:
		return opensightv1.CheckOutcome_CHECK_OUTCOME_NOT_ASSESSED
	}
}

func actionStatusToProto(status store.FindingStatus) opensightv1.ActionStatus {
	switch status {
	case store.FindingOpen:
		return opensightv1.ActionStatus_ACTION_STATUS_OPEN
	case store.FindingDone:
		return opensightv1.ActionStatus_ACTION_STATUS_DONE
	case store.FindingDismissed:
		return opensightv1.ActionStatus_ACTION_STATUS_DISMISSED
	default:
		return opensightv1.ActionStatus_ACTION_STATUS_UNSPECIFIED
	}
}

func actionStatusFromProto(status opensightv1.ActionStatus) store.FindingStatus {
	switch status {
	case opensightv1.ActionStatus_ACTION_STATUS_OPEN:
		return store.FindingOpen
	case opensightv1.ActionStatus_ACTION_STATUS_DONE:
		return store.FindingDone
	case opensightv1.ActionStatus_ACTION_STATUS_DISMISSED:
		return store.FindingDismissed
	default:
		return ""
	}
}

var dismissalReasons = map[string]opensightv1.DismissalReason{
	"NOT_RELEVANT":    opensightv1.DismissalReason_DISMISSAL_REASON_NOT_RELEVANT,
	"ALREADY_DONE":    opensightv1.DismissalReason_DISMISSAL_REASON_ALREADY_DONE,
	"NOT_ACTIONABLE":  opensightv1.DismissalReason_DISMISSAL_REASON_NOT_ACTIONABLE,
	"TOO_MUCH_EFFORT": opensightv1.DismissalReason_DISMISSAL_REASON_TOO_MUCH_EFFORT,
	"OTHER":           opensightv1.DismissalReason_DISMISSAL_REASON_OTHER,
}

func dismissalToProto(reason *string) opensightv1.DismissalReason {
	if reason == nil {
		return opensightv1.DismissalReason_DISMISSAL_REASON_UNSPECIFIED
	}
	return dismissalReasons[*reason]
}

func dismissalFromProto(reason opensightv1.DismissalReason) *string {
	for name, candidate := range dismissalReasons {
		if candidate == reason {
			value := name
			return &value
		}
	}
	return nil
}

// comparisonToProto renders the evidence pair, or nothing at all when the
// finding has none — an action with an empty comparison must not present the
// customer's site as silent on a subject nobody compared it against.
func comparisonToProto(comparison visibility.Comparison) *opensightv1.ActionComparison {
	if comparison.Empty() {
		return nil
	}
	out := &opensightv1.ActionComparison{Coverage: comparison.Coverage, Site: comparison.Site}
	for _, quote := range comparison.Cited {
		out.Cited = append(out.Cited, &opensightv1.CitedQuote{Quote: quote.Quote, Domain: quote.Domain})
	}
	return out
}

func actionToProto(row store.FindingRecord) *opensightv1.ImprovementAction {
	action := &opensightv1.ImprovementAction{
		Comparison: comparisonToProto(row.Comparison),
		Id:         row.ID.String(), Key: row.Key, Source: row.Source,
		Category: row.Category, CategoryLabel: visibility.CategoryLabel(row.Category),
		Title: row.Title, Body: row.Body, Steps: row.Steps, Detail: row.Detail,
		ResultIds: idsToStrings(row.ResultIDs), PromptIds: idsToStrings(row.PromptIDs),
		Sources: row.Sources, Blocking: row.Blocking, Reach: int32(row.Reach),
		Status: actionStatusToProto(row.Status), DismissalReason: dismissalToProto(row.DismissalReason),
		FirstSeenAt: timestamppb.New(row.FirstSeenAt),
	}
	if row.CompletedAt != nil {
		action.CompletedAt = timestamppb.New(*row.CompletedAt)
	}
	return action
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
	rows, err := s.store.ListFindings(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("list actions", err)
	}
	audit, assessed, err := s.store.PublishedAudit(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("load site audit", err)
	}

	resp := &opensightv1.ListActionsResponse{}
	if assessed {
		resp.CheckedAt = timestamppb.New(audit.CheckedAt)
	}
	// Counts cover active work only: the filter is over the queue, and a category
	// whose every action is already done or dismissed is not work left to do.
	active := map[string]int32{}
	for _, row := range rows {
		item := actionToProto(row)
		if row.Status != store.FindingOpen {
			resp.ResolvedActions = append(resp.ResolvedActions, item)
			continue
		}
		resp.Actions = append(resp.Actions, item)
		active[row.Category]++
	}
	for _, category := range visibility.Categories() {
		if active[category.Key] > 0 {
			resp.Categories = append(resp.Categories, &opensightv1.ActionCategory{
				Key: category.Key, Label: category.Label, Count: active[category.Key],
			})
		}
	}
	if len(resp.Actions) == 0 {
		// These two are not interchangeable. Nothing checked yet is silence;
		// nothing found is the absence of evidence, which is never a clean bill
		// of health.
		resp.EmptyReason = opensightv1.ActionsEmptyReason_ACTIONS_EMPTY_REASON_NO_FINDINGS
		if !assessed {
			resp.EmptyReason = opensightv1.ActionsEmptyReason_ACTIONS_EMPTY_REASON_NOT_ASSESSED_YET
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
	row, err := s.store.GetFinding(ctx, su.AccountID, id)
	if err != nil {
		return nil, s.rpcError("get action", err)
	}
	return connect.NewResponse(&opensightv1.GetActionResponse{Action: actionToProto(row)}), nil
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
	row, err := s.store.SetFindingStatus(ctx, su.AccountID, id, status, dismissalFromProto(req.Msg.DismissalReason))
	if err != nil {
		return nil, s.rpcError("set action status", err)
	}
	return connect.NewResponse(&opensightv1.SetActionStatusResponse{Action: actionToProto(row)}), nil
}

// checkOutcomeOrder fixes the order per-outcome counts are reported in, so the
// strip above the checklist does not reshuffle between visits.
var checkOutcomeOrder = []visibility.CheckOutcome{
	visibility.CheckPass, visibility.CheckFail, visibility.CheckCouldNotVerify,
	visibility.CheckNotApplicable, visibility.CheckNotAssessed,
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
	audit, assessed, err := s.store.PublishedAudit(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("get checklist", err)
	}

	// The checklist renders the catalog and overlays the stored results, so a business
	// with no audit yet shows the same rows as everyone else — every check
	// reported as not assessed. Assessed and unassessed take one path.
	outcomes := make(map[string]visibility.CheckResult, len(audit.Checks))
	for _, result := range audit.Checks {
		outcomes[result.Key] = result
	}

	resp := &opensightv1.GetChecklistResponse{Assessed: assessed, Failure: audit.Failure, PagesRead: int32(audit.PagesRead)}
	if assessed {
		resp.CheckedAt = timestamppb.New(audit.CheckedAt)
	}
	counts := map[visibility.CheckOutcome]int32{}
	byGroup := map[string]*opensightv1.CheckGroup{}
	for _, definition := range visibility.Groups() {
		group := &opensightv1.CheckGroup{
			Key: definition.Key, Title: definition.Title,
			Description: definition.Description, References: definition.References,
		}
		byGroup[definition.Key] = group
		resp.Groups = append(resp.Groups, group)
	}
	for _, check := range visibility.Catalog() {
		group, ok := byGroup[check.Group]
		if !ok {
			continue
		}
		result, found := outcomes[check.Key]
		if !found {
			result = visibility.CheckResult{Outcome: visibility.CheckNotAssessed, Detail: "This check has not run for your site yet."}
		}
		group.Checks = append(group.Checks, &opensightv1.Check{
			Key: check.Key, Title: check.Title, What: check.What,
			Outcome:      checkOutcomeToProto(result.Outcome),
			OutcomeLabel: visibility.CheckOutcomeLabel(result.Outcome, check.Informational),
			Detail:       result.Detail, Informational: check.Informational,
		})
		if check.Informational {
			continue
		}
		counts[result.Outcome]++
		group.ChecksTotal++
		if result.Outcome == visibility.CheckPass {
			group.ChecksPassed++
		}
	}
	for _, outcome := range checkOutcomeOrder {
		if counts[outcome] > 0 {
			resp.CheckCounts = append(resp.CheckCounts, &opensightv1.CheckCount{Outcome: checkOutcomeToProto(outcome), Count: counts[outcome]})
		}
	}
	return connect.NewResponse(resp), nil
}
