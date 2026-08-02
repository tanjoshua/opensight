package api

import (
	"context"
	"strings"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
)

var _ opensightv1connect.CompetitorServiceHandler = (*Server)(nil)

const (
	defaultCompetitorLimit = 50
	maxCompetitorLimit     = 100
)

// ListCompetitors serves every competitor's comparison stats, coverage-
// ranked, with an optional status filter and limit/offset pagination.
// GetBusiness is the ownership gate — it must run before
// CompetitorStats, which returns an empty-but-successful result (not an
// error) for an unowned business.
func (s *Server) ListCompetitors(ctx context.Context, req *connect.Request[opensightv1.ListCompetitorsRequest]) (*connect.Response[opensightv1.ListCompetitorsResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "list competitors")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	var statusFilter string
	if req.Msg.Status != opensightv1.CompetitorStatus_COMPETITOR_STATUS_UNSPECIFIED {
		status, ok := competitorStatusFromProto(req.Msg.Status)
		if !ok {
			return nil, rpcInvalidArgument("status must be discovered, tracked, or dismissed")
		}
		statusFilter = string(status)
	}
	limit, offset := rpcPaging(req.Msg.Limit, req.Msg.Offset, defaultCompetitorLimit, maxCompetitorLimit)

	if _, err := s.store.GetBusiness(ctx, su.TenantID, businessID); err != nil {
		return nil, s.rpcError("list competitors", err)
	}

	stats, err := s.metrics.CompetitorStats(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("list competitors: competitor stats", err)
	}

	// Filter by status (display filter), then page. CompetitorStats is
	// already coverage-desc, so the filtered slice keeps that ranking. The
	// [:0:0] re-slice prevents append from clobbering the shared metrics
	// slice.
	filtered := stats.Competitors
	if statusFilter != "" {
		filtered = stats.Competitors[:0:0]
		for _, c := range stats.Competitors {
			if c.Status == statusFilter {
				filtered = append(filtered, c)
			}
		}
	}
	start := min(offset, len(filtered))
	end := start + min(limit, len(filtered)-start)
	page := filtered[start:end]

	resp := &opensightv1.ListCompetitorsResponse{
		Self:        competitorSelfToProto(stats),
		Competitors: make([]*opensightv1.Competitor, 0, len(page)),
		Paging:      &opensightv1.Paging{Limit: int32(limit), Offset: int32(offset), PageCount: int32(len(page))},
	}
	for _, c := range page {
		resp.Competitors = append(resp.Competitors, competitorStatToProto(c))
	}
	return connect.NewResponse(resp), nil
}

// AddCompetitor adds a manually-tracked competitor. Name is validated trimmed
// but stored raw. Blank alias members are not validated here — only
// UpdateCompetitorAliases does that.
func (s *Server) AddCompetitor(ctx context.Context, req *connect.Request[opensightv1.AddCompetitorRequest]) (*connect.Response[opensightv1.AddCompetitorResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "add competitor")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}
	if strings.TrimSpace(req.Msg.Name) == "" {
		return nil, rpcInvalidArgument("name is required")
	}
	var website *string
	if value := strings.TrimSpace(req.Msg.Website); value != "" {
		website = &value
	}

	record, err := s.store.CreateManual(ctx, store.CreateManualCompetitorParams{
		TenantID:   su.TenantID,
		BusinessID: businessID,
		Name:       req.Msg.Name,
		Aliases:    req.Msg.Aliases,
		Website:    website,
	})
	if err != nil {
		return nil, s.rpcError("add competitor: create manual competitor", err)
	}

	return connect.NewResponse(&opensightv1.AddCompetitorResponse{Competitor: competitorRecordToProto(record)}), nil
}

// SetCompetitorStatus merges the track/dismiss pair into one RPC.
// Only TRACKED and DISMISSED are settable; the switch below must catch
// UNSPECIFIED, DISCOVERED, and any unrecognized enum number before the store
// call — the store also rejects DISCOVERED, but with a plain error that
// rpcError maps to CodeInternal, the wrong code for a client fault.
// Re-setting an already-tracked/dismissed competitor is allowed and
// idempotent: the store issues an unconditional UPDATE.
func (s *Server) SetCompetitorStatus(ctx context.Context, req *connect.Request[opensightv1.SetCompetitorStatusRequest]) (*connect.Response[opensightv1.SetCompetitorStatusResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "set competitor status")
	if cerr != nil {
		return nil, cerr
	}
	competitorID, cerr := rpcID("competitor_id", req.Msg.CompetitorId)
	if cerr != nil {
		return nil, cerr
	}

	var status store.CompetitorStatus
	switch req.Msg.Status {
	case opensightv1.CompetitorStatus_COMPETITOR_STATUS_TRACKED:
		status = store.CompetitorStatusTracked
	case opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISMISSED:
		status = store.CompetitorStatusDismissed
	default:
		return nil, rpcInvalidArgument("status must be tracked or dismissed")
	}

	record, err := s.store.SetStatus(ctx, store.SetCompetitorStatusParams{
		TenantID:     su.TenantID,
		CompetitorID: competitorID,
		Status:       status,
	})
	if err != nil {
		return nil, s.rpcError("set competitor status", err)
	}

	return connect.NewResponse(&opensightv1.SetCompetitorStatusResponse{Competitor: competitorRecordToProto(record)}), nil
}

// ReviewSuggestedAlias merges the approve/reject pair into one RPC. The
// decision check runs before the alias check — a request with both an
// invalid decision and a blank alias must report the decision failure (same
// precedent as ReplacePrompt's confirmed-before-text ordering). The alias is
// trimmed here (not left to the store) so a stale/already-consumed alias
// still yields a trimmed value to the store call.
func (s *Server) ReviewSuggestedAlias(ctx context.Context, req *connect.Request[opensightv1.ReviewSuggestedAliasRequest]) (*connect.Response[opensightv1.ReviewSuggestedAliasResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "review suggested alias")
	if cerr != nil {
		return nil, cerr
	}
	competitorID, cerr := rpcID("competitor_id", req.Msg.CompetitorId)
	if cerr != nil {
		return nil, cerr
	}

	approve := false
	switch req.Msg.Decision {
	case opensightv1.AliasDecision_ALIAS_DECISION_APPROVE:
		approve = true
	case opensightv1.AliasDecision_ALIAS_DECISION_REJECT:
		approve = false
	default:
		return nil, rpcInvalidArgument("decision must be approve or reject")
	}

	alias := strings.TrimSpace(req.Msg.Alias)
	if alias == "" {
		return nil, rpcInvalidArgument("alias is required")
	}

	params := store.SuggestedAliasParams{TenantID: su.TenantID, CompetitorID: competitorID, Alias: alias}
	var record store.CompetitorRecord
	var err error
	if approve {
		record, err = s.store.ApproveSuggestedAlias(ctx, params)
	} else {
		record, err = s.store.RejectSuggestedAlias(ctx, params)
	}
	if err != nil {
		return nil, s.rpcError("review suggested alias", err)
	}

	return connect.NewResponse(&opensightv1.ReviewSuggestedAliasResponse{Competitor: competitorRecordToProto(record)}), nil
}

// UpdateCompetitorAliases replaces a competitor's approved alias list. The
// StringList
// wrapper distinguishes three states a bare repeated field cannot: the field
// entirely omitted (nil pointer, 400), present-but-empty (legitimate
// clear-all), and present-with-values (replace).
func (s *Server) UpdateCompetitorAliases(ctx context.Context, req *connect.Request[opensightv1.UpdateCompetitorAliasesRequest]) (*connect.Response[opensightv1.UpdateCompetitorAliasesResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "update competitor aliases")
	if cerr != nil {
		return nil, cerr
	}
	competitorID, cerr := rpcID("competitor_id", req.Msg.CompetitorId)
	if cerr != nil {
		return nil, cerr
	}

	if req.Msg.Aliases == nil {
		return nil, rpcInvalidArgument("aliases is required")
	}
	aliases := req.Msg.Aliases.GetValues()
	if aliases == nil {
		aliases = []string{}
	}
	for _, alias := range aliases {
		if strings.TrimSpace(alias) == "" {
			return nil, rpcInvalidArgument("aliases must not contain blank values")
		}
	}

	record, err := s.store.UpdateAliases(ctx, store.UpdateCompetitorAliasesParams{
		TenantID:     su.TenantID,
		CompetitorID: competitorID,
		Aliases:      aliases,
	})
	if err != nil {
		return nil, s.rpcError("update competitor aliases", err)
	}

	return connect.NewResponse(&opensightv1.UpdateCompetitorAliasesResponse{Competitor: competitorRecordToProto(record)}), nil
}
