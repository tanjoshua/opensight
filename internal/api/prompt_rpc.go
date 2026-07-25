package api

import (
	"context"
	"errors"
	"strings"

	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/metrics"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
)

var _ opensightv1connect.PromptServiceHandler = (*Server)(nil)

// promptStore is the consumer-side seam over *store.PromptStore for the Prompts
// endpoints. ListActivePrompts doubles as the business→tenant ownership gate for
// ListPrompts (ErrNotFound for a missing/cross-tenant business); GetPrompt is
// tenant-scoped and reaches retired prompts, so lineage walks and detail
// lookups can never cross a tenant boundary.
type promptStore interface {
	ListActivePrompts(ctx context.Context, tenantID, businessID domain.ID) ([]store.Prompt, error)
	GetPrompt(ctx context.Context, tenantID, promptID domain.ID) (store.Prompt, error)
	CreateActivePrompt(ctx context.Context, params store.CreateActivePromptParams) (store.Prompt, error)
	ReplacePrompt(ctx context.Context, params store.ReplacePromptParams) (store.Prompt, error)
}

// promptsMetrics is the metrics seam for the Prompts section. Both methods are
// tenant-scoped and compute over the shared analyzed base (MET-1), so a prompt's
// latest-result summary and its spark-trend can never disagree with Overview.
type promptsMetrics interface {
	PromptLatestStats(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.PromptLatest, error)
	PromptTrends(ctx context.Context, tenantID, businessID domain.ID) (map[domain.ID][]metrics.PromptTrendPoint, error)
}

// ListPrompts serves every active prompt with its latest-result summary and
// spark-trend (MET-3). ListActivePrompts is the ownership gate — it must run
// before the metrics calls, which return empty (not an error) for an unowned
// business.
func (s *Server) ListPrompts(ctx context.Context, req *connect.Request[opensightv1.ListPromptsRequest]) (*connect.Response[opensightv1.ListPromptsResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "list prompts")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	prompts, err := s.prompts.ListActivePrompts(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("list prompts: list active prompts", err)
	}

	latest, err := s.promptMetrics.PromptLatestStats(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("list prompts: prompt latest stats", err)
	}
	trends, err := s.promptMetrics.PromptTrends(ctx, su.TenantID, businessID)
	if err != nil {
		return nil, s.rpcError("list prompts: prompt trends", err)
	}

	byPrompt := make(map[domain.ID]metrics.PromptLatest, len(latest))
	for _, l := range latest {
		byPrompt[l.PromptID] = l
	}

	resp := &opensightv1.ListPromptsResponse{Prompts: make([]*opensightv1.PromptSummary, 0, len(prompts))}
	for _, p := range prompts {
		row := &opensightv1.PromptSummary{
			Id:     p.ID.String(),
			Text:   p.Text,
			Status: promptStatusToProto(p.Status),
			Trend:  promptTrendPointsToProto(trends[p.ID]),
		}
		if l, ok := byPrompt[p.ID]; ok {
			resultID := l.ResultID.String()
			row.Mentioned = l.Mentioned
			row.Order = int32Ptr(l.MentionOrder)
			row.Sentiment = sentimentToProto(l.Sentiment)
			row.LatestResultId = &resultID
		}
		resp.Prompts = append(resp.Prompts, row)
	}
	return connect.NewResponse(resp), nil
}

// AddPrompt adds a new active prompt, mirroring handleAddPrompt
// (prompts.go). Text is validated trimmed but stored raw, matching REST's
// existing wart — a data-quality fix is out of scope for this transport
// migration.
func (s *Server) AddPrompt(ctx context.Context, req *connect.Request[opensightv1.AddPromptRequest]) (*connect.Response[opensightv1.AddPromptResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "add prompt")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}
	if strings.TrimSpace(req.Msg.Text) == "" {
		return nil, rpcInvalidArgument("text is required")
	}

	prompt, err := s.prompts.CreateActivePrompt(ctx, store.CreateActivePromptParams{
		TenantID:   su.TenantID,
		BusinessID: businessID,
		Text:       req.Msg.Text,
	})
	if err != nil {
		return nil, s.rpcError("add prompt: create active prompt", err)
	}

	return connect.NewResponse(&opensightv1.AddPromptResponse{Prompt: promptToProto(prompt)}), nil
}

// GetPrompt serves the prompt (active or retired), its full result history,
// and its lineage chain (MET-3), mirroring handleGetPrompt (prompts.go).
// GetPrompt is the tenant gate; the result history is scoped through
// prompt.BusinessID from the fetched row, never the request.
func (s *Server) GetPrompt(ctx context.Context, req *connect.Request[opensightv1.GetPromptRequest]) (*connect.Response[opensightv1.GetPromptResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get prompt")
	if cerr != nil {
		return nil, cerr
	}
	promptID, cerr := rpcID("prompt_id", req.Msg.PromptId)
	if cerr != nil {
		return nil, cerr
	}

	prompt, err := s.prompts.GetPrompt(ctx, su.TenantID, promptID)
	if err != nil {
		return nil, s.rpcError("get prompt", err)
	}

	lineage, err := s.promptLineage(ctx, su.TenantID, prompt)
	if err != nil {
		return nil, s.rpcError("get prompt: lineage", err)
	}

	results, err := s.results.ListResults(ctx, su.TenantID, prompt.BusinessID, store.ResultFilter{PromptID: &promptID})
	if err != nil {
		return nil, s.rpcError("get prompt: list results", err)
	}

	resp := &opensightv1.GetPromptResponse{
		Prompt:  promptToProto(prompt),
		Lineage: make([]*opensightv1.Prompt, 0, len(lineage)),
		Results: make([]*opensightv1.PromptResult, 0, len(results)),
	}
	for _, ancestor := range lineage {
		resp.Lineage = append(resp.Lineage, promptToProto(ancestor))
	}
	for _, item := range results {
		row := promptResultToProto(item.PromptResult)
		row.Unanalyzed = isUnanalyzed(item.Status, item.Analyzed)
		// Prompt/Run/Analysis are left nil: matches REST's
		// resultToResponse(item, false) with no .Prompt assignment here.
		resp.Results = append(resp.Results, row)
	}
	return connect.NewResponse(resp), nil
}

// ReplacePrompt retires the old prompt and inserts a new active prompt
// recording replaces_prompt_id, mirroring handleReplacePrompt (prompts.go).
// The confirmed check comes first, before the text check and before any
// store call: the server-side enforcement of design 06's "structurally
// unskippable" warning, and a request with both an empty text and
// confirmed=false must report the confirmation failure.
func (s *Server) ReplacePrompt(ctx context.Context, req *connect.Request[opensightv1.ReplacePromptRequest]) (*connect.Response[opensightv1.ReplacePromptResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "replace prompt")
	if cerr != nil {
		return nil, cerr
	}
	promptID, cerr := rpcID("prompt_id", req.Msg.PromptId)
	if cerr != nil {
		return nil, cerr
	}
	if !req.Msg.Confirmed {
		return nil, rpcInvalidArgument("confirmed must be true")
	}
	if strings.TrimSpace(req.Msg.Text) == "" {
		return nil, rpcInvalidArgument("text is required")
	}

	prompt, err := s.prompts.ReplacePrompt(ctx, store.ReplacePromptParams{
		TenantID:    su.TenantID,
		OldPromptID: promptID,
		Text:        req.Msg.Text,
	})
	if err != nil {
		return nil, s.rpcError("replace prompt: replace prompt", err)
	}

	return connect.NewResponse(&opensightv1.ReplacePromptResponse{Prompt: promptToProto(prompt)}), nil
}

// promptLineage walks the replaces_prompt_id chain back from prompt, newest
// predecessor first. Each hop is a tenant-scoped GetPrompt, so the chain can
// never cross a tenant. A missing predecessor stops the walk rather than 500-ing
// (data is retained indefinitely, so this is defensive); the visited set guards
// against a malformed cycle.
func (s *Server) promptLineage(ctx context.Context, tenantID domain.ID, prompt store.Prompt) ([]store.Prompt, error) {
	lineage := []store.Prompt{}
	visited := map[domain.ID]bool{prompt.ID: true}
	cur := prompt.ReplacesPromptID
	for cur != nil {
		if visited[*cur] {
			break
		}
		visited[*cur] = true
		ancestor, err := s.prompts.GetPrompt(ctx, tenantID, *cur)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				break
			}
			return nil, err
		}
		lineage = append(lineage, ancestor)
		cur = ancestor.ReplacesPromptID
	}
	return lineage, nil
}
