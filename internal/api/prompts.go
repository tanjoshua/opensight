package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"opensight/internal/domain"
	"opensight/internal/metrics"
	"opensight/internal/store"
)

// promptStore is the consumer-side seam over *store.PromptStore for the Prompts
// endpoints. ListActivePrompts doubles as the business→tenant ownership gate for
// GET /businesses/:id/prompts (ErrNotFound for a missing/cross-tenant business);
// GetPrompt is tenant-scoped and reaches retired prompts, so lineage walks and
// detail lookups can never cross a tenant boundary.
type promptStore interface {
	ListActivePrompts(ctx context.Context, tenantID, businessID domain.ID) ([]store.Prompt, error)
	GetPrompt(ctx context.Context, tenantID, promptID domain.ID) (store.Prompt, error)
}

// promptsMetrics is the metrics seam for the Prompts section. Both methods are
// tenant-scoped and compute over the shared analyzed base (MET-1), so a prompt's
// latest-result summary and its spark-trend can never disagree with Overview.
type promptsMetrics interface {
	PromptLatestStats(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.PromptLatest, error)
	PromptTrends(ctx context.Context, tenantID, businessID domain.ID) (map[domain.ID][]metrics.PromptTrendPoint, error)
}

type promptsListResponse struct {
	Prompts []promptSummaryResponse `json:"prompts"`
}

// promptSummaryResponse is one active-prompt row of the Prompts table: its latest
// analyzed result summary (design 06) plus the spark-trend across runs. Mentioned,
// Order, Sentiment, and LatestResultID come from the latest analyzed result;
// LatestResultID is nil (and Mentioned false) for a prompt with no analyzed result
// yet — the honest "not measured" state, distinct from "analyzed, not mentioned".
type promptSummaryResponse struct {
	ID             string                `json:"id"`
	Text           string                `json:"text"`
	Status         string                `json:"status"`
	Mentioned      bool                  `json:"mentioned"`
	Order          *int                  `json:"order"`
	Sentiment      *string               `json:"sentiment"`
	LatestResultID *string               `json:"latest_result_id"`
	Trend          []promptTrendResponse `json:"trend"`
}

// promptTrendResponse is one point of a prompt's spark-trend: whether the business
// was mentioned in that run, and the result_id door behind it.
type promptTrendResponse struct {
	RunID        string `json:"run_id"`
	ScheduledFor string `json:"scheduled_for"`
	Mentioned    bool   `json:"mentioned"`
	ResultID     string `json:"result_id"`
}

type promptDetailResponse struct {
	Prompt  promptLineageNode   `json:"prompt"`
	Lineage []promptLineageNode `json:"lineage"`
	Results []resultResponse    `json:"results"`
}

// promptLineageNode is a prompt in a replacement chain. ReplacesPromptID links to
// the prompt this one replaced ("replaced X on date", design 06); CreatedAt is the
// date it started its own trend.
type promptLineageNode struct {
	ID               string  `json:"id"`
	Text             string  `json:"text"`
	Status           string  `json:"status"`
	ReplacesPromptID *string `json:"replaces_prompt_id"`
	CreatedAt        string  `json:"created_at"`
}

// handleListPrompts serves GET /businesses/:id/prompts: every active prompt with
// its latest-result summary and spark-trend (MET-3). ListActivePrompts is the
// ownership gate — it returns ErrNotFound for a missing or cross-tenant business,
// so the metrics queries (which return empty for an unowned business) only run
// once ownership is proven.
func (s *Server) handleListPrompts(w http.ResponseWriter, r *http.Request) {
	if s.prompts == nil || s.promptMetrics == nil {
		s.writeInternalError(w, "list prompts: store missing", errors.New("prompt store and metrics are required"))
		return
	}

	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "list prompts: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}

	ctx := r.Context()
	prompts, err := s.prompts.ListActivePrompts(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	latest, err := s.promptMetrics.PromptLatestStats(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	trends, err := s.promptMetrics.PromptTrends(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	byPrompt := make(map[domain.ID]metrics.PromptLatest, len(latest))
	for _, l := range latest {
		byPrompt[l.PromptID] = l
	}

	resp := promptsListResponse{Prompts: make([]promptSummaryResponse, 0, len(prompts))}
	for _, p := range prompts {
		row := promptSummaryResponse{
			ID:     p.ID.String(),
			Text:   p.Text,
			Status: string(p.Status),
			Trend:  trendToResponse(trends[p.ID]),
		}
		if l, ok := byPrompt[p.ID]; ok {
			resultID := l.ResultID.String()
			row.Mentioned = l.Mentioned
			row.Order = l.MentionOrder
			row.Sentiment = l.Sentiment
			row.LatestResultID = &resultID
		}
		resp.Prompts = append(resp.Prompts, row)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetPrompt serves GET /prompts/:id: the prompt (active or retired), its
// full result history, and its lineage chain (MET-3). GetPrompt is the tenant
// gate; the result history and ancestor walk both scope through it.
func (s *Server) handleGetPrompt(w http.ResponseWriter, r *http.Request) {
	if s.prompts == nil || s.results == nil {
		s.writeInternalError(w, "get prompt: store missing", errors.New("prompt and result stores are required"))
		return
	}

	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "get prompt: missing session context", errors.New("missing session context"))
		return
	}
	promptID, ok := pathID(w, r, "promptID")
	if !ok {
		return
	}

	ctx := r.Context()
	prompt, err := s.prompts.GetPrompt(ctx, su.TenantID, promptID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	lineage, err := s.promptLineage(ctx, su.TenantID, prompt)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	results, err := s.results.ListResults(ctx, su.TenantID, prompt.BusinessID, store.ResultFilter{PromptID: &promptID})
	if err != nil {
		writeStoreError(w, err)
		return
	}

	resp := promptDetailResponse{
		Prompt:  promptToLineageNode(prompt),
		Lineage: make([]promptLineageNode, 0, len(lineage)),
		Results: make([]resultResponse, 0, len(results)),
	}
	for _, ancestor := range lineage {
		resp.Lineage = append(resp.Lineage, promptToLineageNode(ancestor))
	}
	for _, item := range results {
		row := resultToResponse(item.PromptResult, false)
		row.Unanalyzed = isUnanalyzed(item.Status, item.Analyzed)
		resp.Results = append(resp.Results, row)
	}
	writeJSON(w, http.StatusOK, resp)
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

func trendToResponse(points []metrics.PromptTrendPoint) []promptTrendResponse {
	out := make([]promptTrendResponse, 0, len(points))
	for _, p := range points {
		out = append(out, promptTrendResponse{
			RunID:        p.RunID.String(),
			ScheduledFor: p.ScheduledFor.Format(time.DateOnly),
			Mentioned:    p.Mentioned,
			ResultID:     p.ResultID.String(),
		})
	}
	return out
}

func promptToLineageNode(p store.Prompt) promptLineageNode {
	node := promptLineageNode{
		ID:        p.ID.String(),
		Text:      p.Text,
		Status:    string(p.Status),
		CreatedAt: formatTime(p.CreatedAt),
	}
	if p.ReplacesPromptID != nil {
		s := p.ReplacesPromptID.String()
		node.ReplacesPromptID = &s
	}
	return node
}
