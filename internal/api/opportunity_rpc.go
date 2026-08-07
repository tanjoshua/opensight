package api

import (
	"context"
	"strings"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/store"
	"opensight/internal/visibility"

	connect "connectrpc.com/connect"
)

var _ opensightv1connect.OpportunityServiceHandler = (*Server)(nil)

func (s *Server) ListOpportunities(ctx context.Context, req *connect.Request[opensightv1.ListOpportunitiesRequest]) (*connect.Response[opensightv1.ListOpportunitiesResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "list opportunities")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}
	rows, err := s.store.ListOpportunities(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("list opportunities", err)
	}
	resp := &opensightv1.ListOpportunitiesResponse{Opportunities: make([]*opensightv1.Opportunity, 0, len(rows))}
	for _, row := range rows {
		resp.Opportunities = append(resp.Opportunities, opportunityToProto(row))
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) GetOpportunity(ctx context.Context, req *connect.Request[opensightv1.GetOpportunityRequest]) (*connect.Response[opensightv1.GetOpportunityResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get opportunity")
	if cerr != nil {
		return nil, cerr
	}
	id, cerr := rpcID("opportunity_id", req.Msg.OpportunityId)
	if cerr != nil {
		return nil, cerr
	}
	row, err := s.store.GetOpportunity(ctx, su.AccountID, id)
	if err != nil {
		return nil, s.rpcError("get opportunity", err)
	}
	return connect.NewResponse(&opensightv1.GetOpportunityResponse{Opportunity: opportunityToProto(row)}), nil
}

func (s *Server) SetOpportunityStatus(ctx context.Context, req *connect.Request[opensightv1.SetOpportunityStatusRequest]) (*connect.Response[opensightv1.SetOpportunityStatusResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "set opportunity status")
	if cerr != nil {
		return nil, cerr
	}
	id, cerr := rpcID("opportunity_id", req.Msg.OpportunityId)
	if cerr != nil {
		return nil, cerr
	}
	status, ok := opportunityStatusFromProto(req.Msg.Status)
	if !ok {
		return nil, rpcInvalidArgument("status is required")
	}
	var reason *string
	if status == store.StatusDismissed {
		value, ok := dismissalFromProto(req.Msg.DismissalReason)
		if !ok {
			return nil, rpcInvalidArgument("dismissal_reason is required when dismissing")
		}
		reason = &value
	}
	row, err := s.store.SetOpportunityStatus(ctx, su.AccountID, id, status, reason)
	if err != nil {
		if strings.Contains(err.Error(), "dismissal reason") || strings.Contains(err.Error(), "invalid opportunity") {
			return nil, rpcInvalidArgument(err.Error())
		}
		return nil, s.rpcError("set opportunity status", err)
	}
	return connect.NewResponse(&opensightv1.SetOpportunityStatusResponse{Opportunity: opportunityToProto(row)}), nil
}

// opportunityToProto carries the store's Current/Focus flags through unchanged:
// they are computed once, in SQL, so every RPC agrees on what a focus item is.
// DirectBlocker comes from the practice catalog, so the UI never has to know a
// practice key.
func opportunityToProto(row store.OpportunityRecord) *opensightv1.Opportunity {
	practice, _ := visibility.Practice(row.PracticeKey)
	out := &opensightv1.Opportunity{Id: row.ID.String(), PracticeKey: row.PracticeKey, SubjectKey: row.SubjectKey, Title: row.Presentation.Title, Summary: row.Presentation.Summary, Effort: row.Presentation.Effort, Status: opportunityStatusToProto(row.UserStatus), AssessmentStatus: string(row.AssessmentStatus), CheckedSources: row.CheckedSources, AssessedAt: row.AssessedAt.UTC().Format("2006-01-02T15:04:05Z"), Current: row.Current, Focus: row.Focus, DirectBlocker: practice.DirectBlocker}
	for _, id := range row.ResultIDs {
		out.ResultIds = append(out.ResultIds, id.String())
	}
	for _, id := range row.PromptIDs {
		out.PromptIds = append(out.PromptIds, id.String())
	}
	if row.DismissalReason != nil {
		out.DismissalReason = dismissalToProto(*row.DismissalReason)
	}
	for _, block := range row.Presentation.Blocks {
		out.Blocks = append(out.Blocks, &opensightv1.OpportunityBlock{Type: blockTypeToProto(block.Type), Title: block.Title, Text: block.Text, Value: block.Value, Url: block.URL, Items: block.Items, ResultIds: block.ResultIDs})
	}
	for _, observation := range row.Observations {
		out.Observations = append(out.Observations, &opensightv1.OutcomeObservation{AssessmentStatus: string(observation.AssessmentStatus), ObservedAt: observation.ObservedAt.UTC().Format("2006-01-02T15:04:05Z"), ResultIds: observation.ResultIDs, PromptIds: observation.PromptIDs})
	}
	return out
}

func opportunityStatusToProto(v store.OpportunityStatus) opensightv1.OpportunityStatus {
	switch v {
	case store.StatusOpen:
		return opensightv1.OpportunityStatus_OPPORTUNITY_STATUS_OPEN
	case store.StatusInProgress:
		return opensightv1.OpportunityStatus_OPPORTUNITY_STATUS_IN_PROGRESS
	case store.StatusCompleted:
		return opensightv1.OpportunityStatus_OPPORTUNITY_STATUS_COMPLETED
	case store.StatusDismissed:
		return opensightv1.OpportunityStatus_OPPORTUNITY_STATUS_DISMISSED
	}
	return opensightv1.OpportunityStatus_OPPORTUNITY_STATUS_UNSPECIFIED
}
func opportunityStatusFromProto(v opensightv1.OpportunityStatus) (store.OpportunityStatus, bool) {
	switch v {
	case opensightv1.OpportunityStatus_OPPORTUNITY_STATUS_OPEN:
		return store.StatusOpen, true
	case opensightv1.OpportunityStatus_OPPORTUNITY_STATUS_IN_PROGRESS:
		return store.StatusInProgress, true
	case opensightv1.OpportunityStatus_OPPORTUNITY_STATUS_COMPLETED:
		return store.StatusCompleted, true
	case opensightv1.OpportunityStatus_OPPORTUNITY_STATUS_DISMISSED:
		return store.StatusDismissed, true
	}
	return "", false
}
func dismissalToProto(v string) opensightv1.DismissalReason {
	m := map[string]opensightv1.DismissalReason{"NOT_RELEVANT": opensightv1.DismissalReason_DISMISSAL_REASON_NOT_RELEVANT, "ALREADY_DONE": opensightv1.DismissalReason_DISMISSAL_REASON_ALREADY_DONE, "NOT_ACTIONABLE": opensightv1.DismissalReason_DISMISSAL_REASON_NOT_ACTIONABLE, "TOO_MUCH_EFFORT": opensightv1.DismissalReason_DISMISSAL_REASON_TOO_MUCH_EFFORT, "OTHER": opensightv1.DismissalReason_DISMISSAL_REASON_OTHER}
	return m[v]
}
func dismissalFromProto(v opensightv1.DismissalReason) (string, bool) {
	for key, value := range map[string]opensightv1.DismissalReason{"NOT_RELEVANT": opensightv1.DismissalReason_DISMISSAL_REASON_NOT_RELEVANT, "ALREADY_DONE": opensightv1.DismissalReason_DISMISSAL_REASON_ALREADY_DONE, "NOT_ACTIONABLE": opensightv1.DismissalReason_DISMISSAL_REASON_NOT_ACTIONABLE, "TOO_MUCH_EFFORT": opensightv1.DismissalReason_DISMISSAL_REASON_TOO_MUCH_EFFORT, "OTHER": opensightv1.DismissalReason_DISMISSAL_REASON_OTHER} {
		if value == v {
			return key, true
		}
	}
	return "", false
}
func blockTypeToProto(v visibility.BlockType) opensightv1.OpportunityBlockType {
	m := map[visibility.BlockType]opensightv1.OpportunityBlockType{visibility.BlockText: opensightv1.OpportunityBlockType_OPPORTUNITY_BLOCK_TYPE_TEXT, visibility.BlockMetric: opensightv1.OpportunityBlockType_OPPORTUNITY_BLOCK_TYPE_METRIC, visibility.BlockLink: opensightv1.OpportunityBlockType_OPPORTUNITY_BLOCK_TYPE_LINK, visibility.BlockQuestionList: opensightv1.OpportunityBlockType_OPPORTUNITY_BLOCK_TYPE_QUESTION_LIST, visibility.BlockEvidenceList: opensightv1.OpportunityBlockType_OPPORTUNITY_BLOCK_TYPE_EVIDENCE_LIST, visibility.BlockNotice: opensightv1.OpportunityBlockType_OPPORTUNITY_BLOCK_TYPE_NOTICE}
	return m[v]
}
