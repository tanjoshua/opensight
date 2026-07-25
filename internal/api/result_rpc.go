package api

import (
	"context"

	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/metrics"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
)

var _ opensightv1connect.ResultServiceHandler = (*Server)(nil)

const (
	defaultResultLimit = 50
	maxResultLimit     = 100
)

type runStore interface {
	ListRuns(ctx context.Context, tenantID, businessID domain.ID) ([]store.Run, error)
}

// runsMetrics is the metrics seam for the Runs endpoint: per-run visibility %
// comes from the same shared analyzed base as Overview (MET-1), so a run's
// visibility can never disagree with the trend line.
type runsMetrics interface {
	VisibilityTrend(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.VisibilityPoint, error)
}

type resultStore interface {
	ListResults(ctx context.Context, tenantID, businessID domain.ID, filter store.ResultFilter) ([]store.ResultListItem, error)
	GetResultDetail(ctx context.Context, tenantID, resultID domain.ID) (store.ResultDetail, error)
	GetResultAnalysis(ctx context.Context, tenantID, resultID domain.ID) (store.ResultAnalysis, error)
}

// ListRuns serves every monitoring run for a business with its per-run
// visibility %. ListRuns is the ownership gate — it must run before
// VisibilityTrend, which does not error for an unowned business. This is the
// only place Run.Visibility gets populated: runToProto itself always leaves
// it nil.
func (s *Server) ListRuns(ctx context.Context, req *connect.Request[opensightv1.ListRunsRequest]) (*connect.Response[opensightv1.ListRunsResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "list runs")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	runs, err := s.runs.ListRuns(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("list runs", err)
	}

	points, err := s.runMetrics.VisibilityTrend(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("list runs: visibility trend", err)
	}
	visibility := make(map[domain.ID]float64, len(points))
	for _, p := range points {
		visibility[p.RunID] = p.Percent
	}

	resp := &opensightv1.ListRunsResponse{Runs: make([]*opensightv1.Run, 0, len(runs))}
	for _, run := range runs {
		row := runToProto(run)
		if pct, ok := visibility[run.ID]; ok {
			row.Visibility = &pct
		}
		resp.Runs = append(resp.Runs, row)
	}
	return connect.NewResponse(resp), nil
}

// resultFilterFromProto parses ListResultsRequest into a store.ResultFilter
// plus the normalized limit/offset, mirroring resultFilterFromRequest
// (responses.go). Returns a concrete *connect.Error (not error) — callers
// must check `if cerr != nil`.
func resultFilterFromProto(msg *opensightv1.ListResultsRequest) (store.ResultFilter, int, int, *connect.Error) {
	limit, offset := rpcPaging(msg.Limit, msg.Offset, defaultResultLimit, maxResultLimit)
	filter := store.ResultFilter{Limit: limit, Offset: offset}

	if msg.RunId != "" {
		id, cerr := rpcID("run_id", msg.RunId)
		if cerr != nil {
			return store.ResultFilter{}, 0, 0, cerr
		}
		filter.RunID = &id
	}
	if msg.PromptId != "" {
		id, cerr := rpcID("prompt_id", msg.PromptId)
		if cerr != nil {
			return store.ResultFilter{}, 0, 0, cerr
		}
		filter.PromptID = &id
	}
	switch msg.Status {
	case opensightv1.ResultStatus_RESULT_STATUS_UNSPECIFIED:
		// no filter
	case opensightv1.ResultStatus_RESULT_STATUS_SUCCEEDED:
		status := store.ResultStatusSucceeded
		filter.Status = &status
	case opensightv1.ResultStatus_RESULT_STATUS_FAILED:
		status := store.ResultStatusFailed
		filter.Status = &status
	default:
		return store.ResultFilter{}, 0, 0, rpcInvalidArgument("status must be succeeded or failed")
	}
	if msg.Mentioned != nil {
		m := *msg.Mentioned
		filter.Mentioned = &m
	}

	return filter, limit, offset, nil
}

// ListResults serves a business's results with optional filters and
// limit/offset pagination, mirroring handleListResults (responses.go).
// ListResults has its own internal ownership check, so no separate
// GetBusiness call is needed here (unlike ListCompetitors/ListRuns).
func (s *Server) ListResults(ctx context.Context, req *connect.Request[opensightv1.ListResultsRequest]) (*connect.Response[opensightv1.ListResultsResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "list results")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}
	filter, limit, offset, cerr := resultFilterFromProto(req.Msg)
	if cerr != nil {
		return nil, cerr
	}

	results, err := s.results.ListResults(ctx, su.TenantID, businessID, filter)
	if err != nil {
		return nil, s.rpcError("list results", err)
	}

	resp := &opensightv1.ListResultsResponse{
		Results: make([]*opensightv1.PromptResult, 0, len(results)),
		Paging:  &opensightv1.Paging{Limit: int32(limit), Offset: int32(offset), PageCount: int32(len(results))},
	}
	for _, item := range results {
		row := promptResultToProto(item.PromptResult)
		row.Unanalyzed = isUnanalyzed(item.Status, item.Analyzed)
		row.Prompt = &opensightv1.PromptRef{Id: item.PromptID.String(), Text: item.PromptText}
		resp.Results = append(resp.Results, row)
	}
	return connect.NewResponse(resp), nil
}

// GetResult serves the Response drawer's full detail — the result, its
// prompt/run context, and (when analyzed) its evidence — mirroring
// handleGetResult (responses.go). GetResultDetail is the tenant gate.
func (s *Server) GetResult(ctx context.Context, req *connect.Request[opensightv1.GetResultRequest]) (*connect.Response[opensightv1.GetResultResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get result")
	if cerr != nil {
		return nil, cerr
	}
	resultID, cerr := rpcID("result_id", req.Msg.ResultId)
	if cerr != nil {
		return nil, cerr
	}

	detail, err := s.results.GetResultDetail(ctx, su.TenantID, resultID)
	if err != nil {
		return nil, s.rpcError("get result", err)
	}
	analysis, err := s.results.GetResultAnalysis(ctx, su.TenantID, resultID)
	if err != nil {
		return nil, s.rpcError("get result: analysis", err)
	}

	row := promptResultToProto(detail.Result)
	if req.Msg.IncludeRaw {
		row.RawResponseJson = string(detail.Result.RawResponse)
	}
	row.Unanalyzed = isUnanalyzed(detail.Result.Status, analysis.Analyzed)
	row.Prompt = &opensightv1.PromptRef{Id: detail.Prompt.ID.String(), Text: detail.Prompt.Text}
	row.Run = runToProto(detail.Run)
	if analysis.Analyzed {
		row.Analysis = resultAnalysisToProto(analysis, detail.Result.RawResponse)
	}

	return connect.NewResponse(&opensightv1.GetResultResponse{Result: row}), nil
}

// isUnanalyzed is the "not yet analyzed" badge rule: a succeeded result with no
// analysis row. Failed results are never flagged this way — they show an error.
func isUnanalyzed(status store.ResultStatus, analyzed bool) bool {
	return status == store.ResultStatusSucceeded && !analyzed
}
