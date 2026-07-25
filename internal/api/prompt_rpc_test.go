package api

import (
	"testing"
	"time"

	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/metrics"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
)

// newPromptRPCServer builds a Server wired for direct PromptService method
// calls (no HTTP/session interceptor involved — the session user is injected
// into ctx via businessRPCContext instead).
func newPromptRPCServer(prompts promptStore, results resultStore, m promptsMetrics) *Server {
	return &Server{prompts: prompts, results: results, promptMetrics: m}
}

// TestRPCListPromptsShapesSummaryAndTrend ports
// TestListPromptsShapesSummaryAndTrend: an analyzed prompt carries its latest
// summary + result_id door and its spark-trend; an active prompt with no
// analyzed result yet reports the honest "not measured" state (null
// latest_result_id, empty trend), not a false absence.
func TestRPCListPromptsShapesSummaryAndTrend(t *testing.T) {
	analyzed := mustHashV7(t, promptIDForTest)
	fresh := mustHashV7(t, "01950000-0000-7000-8000-0000000001a1")
	runID := mustHashV7(t, runIDForTest)
	resultID := mustHashV7(t, resultIDForTest)
	order := 0
	sentiment := "positive"

	prompts := &fakePromptStore{active: []store.Prompt{
		{ID: analyzed, Text: "best clinic near me", Status: store.PromptStatusActive},
		{ID: fresh, Text: "top rated clinic", Status: store.PromptStatusActive},
	}}
	m := &fakePromptsMetrics{
		latest: []metrics.PromptLatest{
			{PromptID: analyzed, ResultID: resultID, Mentioned: true, MentionOrder: &order, Sentiment: &sentiment},
		},
		trends: map[domain.ID][]metrics.PromptTrendPoint{
			analyzed: {{RunID: runID, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Mentioned: true, ResultID: resultID}},
		},
	}

	srv := newPromptRPCServer(prompts, &fakeResultStore{}, m)
	resp, err := srv.ListPrompts(businessRPCContext(t), connect.NewRequest(&opensightv1.ListPromptsRequest{BusinessId: businessIDForTest}))
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	got := resp.Msg.GetPrompts()
	if len(got) != 2 {
		t.Fatalf("prompts = %d, want 2", len(got))
	}

	row := got[0]
	if row.GetId() != analyzed.String() || !row.GetMentioned() || row.Order == nil || *row.Order != 0 ||
		row.GetSentiment() != opensightv1.Sentiment_SENTIMENT_POSITIVE {
		t.Fatalf("analyzed prompt summary = %+v", row)
	}
	if row.LatestResultId == nil || *row.LatestResultId != resultID.String() {
		t.Fatalf("analyzed prompt latest_result_id = %v, want door to %s", row.LatestResultId, resultID)
	}
	if trend := row.GetTrend(); len(trend) != 1 || trend[0].GetScheduledFor() != "2026-07-13" || !trend[0].GetMentioned() || trend[0].GetResultId() != resultID.String() {
		t.Fatalf("analyzed prompt trend = %+v", row.GetTrend())
	}

	fr := got[1]
	if fr.GetId() != fresh.String() || fr.GetMentioned() || fr.Order != nil ||
		fr.GetSentiment() != opensightv1.Sentiment_SENTIMENT_UNSPECIFIED || fr.LatestResultId != nil || len(fr.GetTrend()) != 0 {
		t.Fatalf("unanalyzed prompt summary = %+v, want not-measured state", fr)
	}
}

// TestRPCListPromptsNotFoundForCrossTenant confirms ListActivePrompts is the
// ownership gate: its ErrNotFound becomes NotFound before any metric runs.
func TestRPCListPromptsNotFoundForCrossTenant(t *testing.T) {
	prompts := &fakePromptStore{listErr: store.ErrNotFound}
	srv := newPromptRPCServer(prompts, &fakeResultStore{}, &fakePromptsMetrics{})
	_, err := srv.ListPrompts(businessRPCContext(t), connect.NewRequest(&opensightv1.ListPromptsRequest{BusinessId: businessIDForTest}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

// TestRPCGetPromptWalksLineageAndHistory ports
// TestGetPromptDetailWalksLineageAndHistory: the retired prompt is reachable,
// its full result history is returned, and its lineage chain is walked back
// through replaces_prompt_id.
func TestRPCGetPromptWalksLineageAndHistory(t *testing.T) {
	oldest := mustHashV7(t, "01950000-0000-7000-8000-0000000001b1")
	middle := mustHashV7(t, "01950000-0000-7000-8000-0000000001b2")
	current := mustHashV7(t, promptIDForTest)
	businessID := mustHashV7(t, businessIDForTest)
	runID := mustHashV7(t, runIDForTest)
	resultID := mustHashV7(t, resultIDForTest)

	prompts := &fakePromptStore{byID: map[domain.ID]store.Prompt{
		oldest:  {ID: oldest, BusinessID: businessID, Text: "v1", Status: store.PromptStatusRetired, CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		middle:  {ID: middle, BusinessID: businessID, Text: "v2", Status: store.PromptStatusRetired, ReplacesPromptID: &oldest, CreatedAt: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)},
		current: {ID: current, BusinessID: businessID, Text: "v3", Status: store.PromptStatusActive, ReplacesPromptID: &middle, CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
	}}
	results := &fakeResultStore{results: []store.ResultListItem{{
		PromptResult: store.PromptResult{
			ID: resultID, RunID: runID, PromptID: current, Status: store.ResultStatusSucceeded,
			ResponseText: ptrString("Acme Clinic is recommended."),
			RequestedAt:  time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC),
			CompletedAt:  time.Date(2026, 7, 13, 11, 1, 0, 0, time.UTC),
		},
		PromptText: "v3",
	}}}

	srv := newPromptRPCServer(prompts, results, &fakePromptsMetrics{})
	resp, err := srv.GetPrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.GetPromptRequest{PromptId: promptIDForTest}))
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	body := resp.Msg

	if body.GetPrompt().GetId() != current.String() || body.GetPrompt().ReplacesPromptId == nil || *body.GetPrompt().ReplacesPromptId != middle.String() {
		t.Fatalf("prompt node = %+v", body.GetPrompt())
	}
	lineage := body.GetLineage()
	if len(lineage) != 2 || lineage[0].GetId() != middle.String() || lineage[1].GetId() != oldest.String() {
		t.Fatalf("lineage = %+v, want middle then oldest", lineage)
	}
	if lineage[1].ReplacesPromptId != nil {
		t.Fatalf("oldest node replaces = %v, want nil", lineage[1].ReplacesPromptId)
	}
	if results := body.GetResults(); len(results) != 1 || results[0].GetId() != resultID.String() {
		t.Fatalf("results = %+v", results)
	}
	if results.gotFilter.PromptID == nil || *results.gotFilter.PromptID != current {
		t.Fatalf("result filter prompt = %v, want %s", results.gotFilter.PromptID, current)
	}
	if results.gotBusiness != businessID {
		t.Fatalf("results scoped to business %s, want %s", results.gotBusiness, businessID)
	}
}

// TestRPCGetPromptNotFoundForCrossTenant confirms GetPrompt is the tenant
// gate: its ErrNotFound becomes NotFound before any history or lineage is
// fetched.
func TestRPCGetPromptNotFoundForCrossTenant(t *testing.T) {
	prompts := &fakePromptStore{getErr: store.ErrNotFound}
	results := &fakeResultStore{}
	srv := newPromptRPCServer(prompts, results, &fakePromptsMetrics{})
	_, err := srv.GetPrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.GetPromptRequest{PromptId: promptIDForTest}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
	if results.listCalled != 0 {
		t.Fatalf("ListResults called %d times before ownership proven, want 0", results.listCalled)
	}
}

// TestRPCAddPromptCreates pins the add happy path: a non-empty text creates
// an active prompt and the store is called with the session tenant + request
// business.
func TestRPCAddPromptCreates(t *testing.T) {
	newID := mustHashV7(t, "01950000-0000-7000-8000-0000000002a1")
	prompts := &fakePromptStore{createResult: store.Prompt{ID: newID, Text: "best clinic near me", Status: store.PromptStatusActive}}
	srv := newPromptRPCServer(prompts, &fakeResultStore{}, &fakePromptsMetrics{})

	resp, err := srv.AddPrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.AddPromptRequest{
		BusinessId: businessIDForTest, Text: "best clinic near me",
	}))
	if err != nil {
		t.Fatalf("AddPrompt: %v", err)
	}
	if resp.Msg.GetPrompt().GetId() != newID.String() {
		t.Fatalf("prompt id = %s, want %s", resp.Msg.GetPrompt().GetId(), newID)
	}
	if prompts.createParams.TenantID != mustHashV7(t, tenantID) {
		t.Fatalf("create tenant = %s, want session tenant", prompts.createParams.TenantID)
	}
	if prompts.createParams.BusinessID != mustHashV7(t, businessIDForTest) {
		t.Fatalf("create business = %s, want request business", prompts.createParams.BusinessID)
	}
}

func TestRPCAddPromptEmptyTextRejected(t *testing.T) {
	prompts := &fakePromptStore{}
	srv := newPromptRPCServer(prompts, &fakeResultStore{}, &fakePromptsMetrics{})
	_, err := srv.AddPrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.AddPromptRequest{
		BusinessId: businessIDForTest, Text: "  ",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

// TestRPCAddPromptAtLimitConflicts confirms the plan limit maps to
// ResourceExhausted, not a generic conflict shape.
func TestRPCAddPromptAtLimitConflicts(t *testing.T) {
	prompts := &fakePromptStore{createErr: store.ErrPromptLimitExceeded}
	srv := newPromptRPCServer(prompts, &fakeResultStore{}, &fakePromptsMetrics{})
	_, err := srv.AddPrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.AddPromptRequest{
		BusinessId: businessIDForTest, Text: "one more prompt",
	}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", connect.CodeOf(err))
	}
}

// TestRPCAddPromptCrossTenant confirms a missing/cross-tenant business is
// NotFound (the store's ownership gate).
func TestRPCAddPromptCrossTenant(t *testing.T) {
	prompts := &fakePromptStore{createErr: store.ErrNotFound}
	srv := newPromptRPCServer(prompts, &fakeResultStore{}, &fakePromptsMetrics{})
	_, err := srv.AddPrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.AddPromptRequest{
		BusinessId: businessIDForTest, Text: "best clinic near me",
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

// TestRPCReplacePromptCreates pins the replace happy path: the new prompt
// records replaces_prompt_id and the store is called with the request prompt
// as OldPromptID.
func TestRPCReplacePromptCreates(t *testing.T) {
	oldID := mustHashV7(t, promptIDForTest)
	newID := mustHashV7(t, "01950000-0000-7000-8000-0000000002b1")
	prompts := &fakePromptStore{replaceResult: store.Prompt{ID: newID, Text: "new text", Status: store.PromptStatusActive, ReplacesPromptID: &oldID}}
	srv := newPromptRPCServer(prompts, &fakeResultStore{}, &fakePromptsMetrics{})

	resp, err := srv.ReplacePrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.ReplacePromptRequest{
		PromptId: promptIDForTest, Text: "new text", Confirmed: true,
	}))
	if err != nil {
		t.Fatalf("ReplacePrompt: %v", err)
	}
	got := resp.Msg.GetPrompt()
	if got.GetId() != newID.String() {
		t.Fatalf("prompt id = %s, want new prompt %s", got.GetId(), newID)
	}
	if got.ReplacesPromptId == nil || *got.ReplacesPromptId != oldID.String() {
		t.Fatalf("replaces_prompt_id = %v, want %s", got.ReplacesPromptId, oldID)
	}
	if prompts.replaceParams.OldPromptID != oldID {
		t.Fatalf("replace old prompt = %s, want request prompt %s", prompts.replaceParams.OldPromptID, oldID)
	}
}

// TestRPCReplacePromptRequiresConfirmed is the server-side enforcement of the
// unskippable warning: confirmed:false is rejected before the store is
// touched, even with an also-invalid empty text.
func TestRPCReplacePromptRequiresConfirmed(t *testing.T) {
	prompts := &fakePromptStore{}
	srv := newPromptRPCServer(prompts, &fakeResultStore{}, &fakePromptsMetrics{})
	_, err := srv.ReplacePrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.ReplacePromptRequest{
		PromptId: promptIDForTest, Text: "", Confirmed: false,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
	if prompts.replaceParams.OldPromptID != (domain.ID{}) {
		t.Fatalf("store called despite confirmed:false")
	}
}

// TestRPCReplacePromptRetiredConflicts confirms replacing a non-active prompt
// maps to FailedPrecondition, exercising the new ErrPromptNotActive sentinel
// arm in rpcError.
func TestRPCReplacePromptRetiredConflicts(t *testing.T) {
	prompts := &fakePromptStore{replaceErr: store.ErrPromptNotActive}
	srv := newPromptRPCServer(prompts, &fakeResultStore{}, &fakePromptsMetrics{})
	_, err := srv.ReplacePrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.ReplacePromptRequest{
		PromptId: promptIDForTest, Text: "new text", Confirmed: true,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

// TestRPCReplacePromptCrossTenant confirms a missing/cross-tenant prompt is
// NotFound.
func TestRPCReplacePromptCrossTenant(t *testing.T) {
	prompts := &fakePromptStore{replaceErr: store.ErrNotFound}
	srv := newPromptRPCServer(prompts, &fakeResultStore{}, &fakePromptsMetrics{})
	_, err := srv.ReplacePrompt(businessRPCContext(t), connect.NewRequest(&opensightv1.ReplacePromptRequest{
		PromptId: promptIDForTest, Text: "new text", Confirmed: true,
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}
