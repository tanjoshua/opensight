package api

import (
	"context"
	"slices"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/metrics"

	connect "connectrpc.com/connect"
)

var _ opensightv1connect.CitationServiceHandler = (*Server)(nil)

const (
	defaultCitationLimit = 50
	maxCitationLimit     = 100
)

// ListCitationSources serves the citation-sources drill-down.
// GetBusiness is the ownership gate before the metrics query runs —
// CitationSources returns empty (not an error) for an unowned business.
func (s *Server) ListCitationSources(ctx context.Context, req *connect.Request[opensightv1.ListCitationSourcesRequest]) (*connect.Response[opensightv1.ListCitationSourcesResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "list citations")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}
	domainFilter := req.Msg.Domain
	limit, offset := rpcPaging(req.Msg.Limit, req.Msg.Offset, defaultCitationLimit, maxCitationLimit)

	if _, err := s.store.GetBusiness(ctx, su.TenantID, businessID); err != nil {
		return nil, s.rpcError("list citations", err)
	}

	sources, err := s.metrics.CitationSources(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("list citations: citation sources", err)
	}

	filtered := sources
	if domainFilter != "" {
		filtered = slices.DeleteFunc(slices.Clone(sources), func(source metrics.CitationSource) bool {
			return source.Domain != domainFilter
		})
	}
	start := min(offset, len(filtered))
	end := start + min(limit, len(filtered)-start)
	page := filtered[start:end]

	resp := &opensightv1.ListCitationSourcesResponse{
		Domains: make([]*opensightv1.CitationSource, 0, len(page)),
		Paging:  &opensightv1.Paging{Limit: int32(limit), Offset: int32(offset), PageCount: int32(len(page))},
	}
	for _, source := range page {
		resp.Domains = append(resp.Domains, citationSourceToProto(source))
	}
	return connect.NewResponse(resp), nil
}
