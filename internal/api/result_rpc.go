package api

import (
	"context"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/jobs"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ opensightv1connect.ResultServiceHandler = (*Server)(nil)

const (
	defaultResultLimit = 50
	maxResultLimit     = 100
)

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

	runs, err := s.store.ListRuns(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("list runs", err)
	}

	points, err := s.metrics.VisibilityTrend(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("list runs: visibility trend", err)
	}
	visibility := make(map[domain.ID]float64, len(points))
	for _, p := range points {
		visibility[p.RunID] = p.Percent
	}

	resp := &opensightv1.ListRunsResponse{Runs: make([]*opensightv1.Run, 0, len(runs))}
	for _, run := range runs {
		row := runListItemToProto(run)
		if pct, ok := visibility[run.ID]; ok {
			row.Visibility = &pct
		}
		resp.Runs = append(resp.Runs, row)
	}
	resp.NextRunAt = s.nextRunAt(ctx, su.AccountID, businessID)
	return connect.NewResponse(resp), nil
}

// nextRunAt looks up the business's monitoring Schedule for its next fire
// time. It is best-effort: a nil river client (handler unit
// tests that don't wire one), a missing schedule, or an unreachable River
// all return nil rather than failing the ListRuns request — this is a "next
// run" hint, not a correctness-critical field. Given a short deadline so a
// slow or unreachable River cannot stall the poll, and logged at Warn
// (not s.rpcError, which implies a failed request) on error.
func (s *Server) nextRunAt(ctx context.Context, accountID, businessID domain.ID) *timestamppb.Timestamp {
	business, err := s.store.GetBusiness(ctx, accountID, businessID)
	if err != nil || business.Status != store.BusinessStatusActive {
		return nil
	}
	sub, err := s.store.GetByAccount(ctx, accountID)
	if err != nil || !billing.DeriveAccess(sub.AccessState(), time.Now().UTC()).Active() {
		return nil
	}
	next := jobs.NextWeeklySlot(businessID, time.Now().UTC())
	return timestamppb.New(next)
}

// resultFilterFromProto parses ListResultsRequest into a store.ResultFilter
// plus the normalized limit/offset. Returns a concrete *connect.Error (not
// error) — callers
// must check `if cerr != nil`.
func resultFilterFromProto(msg *opensightv1.ListResultsRequest) (store.ResultFilter, int, int, *connect.Error) {
	limit, offset := rpcPaging(msg.Limit, msg.Offset, defaultResultLimit, maxResultLimit)
	filter := store.ResultFilter{Limit: limit, Offset: offset}

	if len(msg.ResultIds) > 0 {
		filter.ResultIDs = make([]domain.ID, 0, len(msg.ResultIds))
		for _, rawID := range msg.ResultIds {
			id, cerr := rpcID("result_ids", rawID)
			if cerr != nil {
				return store.ResultFilter{}, 0, 0, cerr
			}
			filter.ResultIDs = append(filter.ResultIDs, id)
		}
	}
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
// limit/offset pagination. ListResults has its own internal ownership check, so no separate
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

	results, err := s.store.ListResults(ctx, su.AccountID, businessID, filter)
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
		row.Mentioned = mentionedOrNil(item)
		row.Prompt = &opensightv1.PromptRef{Id: item.PromptID.String(), Text: item.PromptText}
		resp.Results = append(resp.Results, row)
	}
	return connect.NewResponse(resp), nil
}

// GetResult serves the Response drawer's full detail — the result, its
// prompt/run context, and (when analyzed) its evidence. GetResultDetail is
// the account gate.
func (s *Server) GetResult(ctx context.Context, req *connect.Request[opensightv1.GetResultRequest]) (*connect.Response[opensightv1.GetResultResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get result")
	if cerr != nil {
		return nil, cerr
	}
	resultID, cerr := rpcID("result_id", req.Msg.ResultId)
	if cerr != nil {
		return nil, cerr
	}

	detail, err := s.store.GetResultDetail(ctx, su.AccountID, resultID)
	if err != nil {
		return nil, s.rpcError("get result", err)
	}
	analysis, err := s.store.GetResultAnalysis(ctx, su.AccountID, resultID)
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

// mentionedOrNil carries the self-mention verdict only for results that have
// one. An unanalyzed or failed result reads SelfMentioned=false in SQL simply
// because it has no mentions rows yet, which must not be published as an
// analyzed "not mentioned" (05's soft-failure posture).
func mentionedOrNil(item store.ResultListItem) *bool {
	if !item.Analyzed || item.Status != store.ResultStatusSucceeded {
		return nil
	}
	return &item.SelfMentioned
}
