package api

import (
	"context"

	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/metrics"

	connect "connectrpc.com/connect"
)

var _ opensightv1connect.OverviewServiceHandler = (*Server)(nil)

// overviewMetrics is the consumer-side seam over *metrics.Metrics so the handler
// unit-tests against a fake. Every method is tenant-scoped and computes over the
// shared analyzed base (MET-1), so Overview can never disagree with Prompts or
// Competitors on what visibility means.
type overviewMetrics interface {
	VisibilityTrend(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.VisibilityPoint, error)
	KeywordStats(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.KeywordStat, error)
	CitationDomainStats(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.DomainStat, error)
	CompetitorStats(ctx context.Context, tenantID, businessID domain.ID) (metrics.CompetitorStats, error)
	PromptChanges(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.PromptChange, error)
}

// GetOverview assembles the single Overview payload from the shared metrics
// package (MET-2). ListRuns doubles as the business->tenant ownership gate
// and must run before any metrics call: metrics.* return empty (not an
// error) for an unowned business, so calling them first would leak a 200
// with an empty-but-valid overview for someone else's business instead of a
// 404.
func (s *Server) GetOverview(ctx context.Context, req *connect.Request[opensightv1.GetOverviewRequest]) (*connect.Response[opensightv1.GetOverviewResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get overview")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	runs, err := s.runs.ListRuns(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("get overview: list runs", err)
	}

	trend, err := s.metrics.VisibilityTrend(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("get overview: visibility trend", err)
	}
	keywords, err := s.metrics.KeywordStats(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("get overview: keyword stats", err)
	}
	domains, err := s.metrics.CitationDomainStats(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("get overview: citation domain stats", err)
	}
	competitors, err := s.metrics.CompetitorStats(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("get overview: competitor stats", err)
	}
	changes, err := s.metrics.PromptChanges(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("get overview: prompt changes", err)
	}

	topCompetitors, discoveredTotal := topCompetitorsToProto(competitors)
	resp := &opensightv1.GetOverviewResponse{
		Visibility:      visibilitySummaryToProto(trend),
		PromptChanges:   promptChangesToProto(changes),
		TopKeywords:     keywordStatsToProto(keywords),
		TopCitedDomains: domainStatsToProto(domains),
		TopCompetitors:  topCompetitors,
		DiscoveredTotal: int32(discoveredTotal),
	}
	if len(runs) > 0 {
		resp.LatestRun = runListItemToProto(runs[0])
	}
	return connect.NewResponse(resp), nil
}
