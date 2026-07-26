package api

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/metrics"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
)

// newResultRPCServer builds a Server wired for direct ResultService method
// calls (no HTTP/session interceptor involved — the session user is injected
// into ctx via businessRPCContext instead).
func newResultRPCServer(runs runStore, results resultStore, m runsMetrics) *Server {
	return &Server{runs: runs, results: results, runMetrics: m}
}

// TestRPCListRunsShapesPayload ports TestListRunsEndpoint.
func TestRPCListRunsShapesPayload(t *testing.T) {
	runID := mustHashV7(t, runIDForTest)
	businessID := mustHashV7(t, businessIDForTest)
	completedAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	runs := &fakeRunStore{runs: []store.RunListItem{{Run: store.Run{
		ID:           runID,
		BusinessID:   businessID,
		Platform:     "chatgpt",
		Trigger:      store.RunTriggerScheduled,
		ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
		Status:       store.RunStatusCompleted,
		WorkflowID:   "run-" + runIDForTest,
		StartedAt:    time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC),
		CompletedAt:  &completedAt,
	}}}}
	srv := newResultRPCServer(runs, &fakeResultStore{}, &fakeRunsMetrics{})

	resp, err := srv.ListRuns(businessRPCContext(t), connect.NewRequest(&opensightv1.ListRunsRequest{BusinessId: businessIDForTest}))
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	got := resp.Msg.GetRuns()
	if len(got) != 1 {
		t.Fatalf("runs = %d, want 1", len(got))
	}
	row := got[0]
	if row.GetId() != runID.String() || row.GetPlatform() != "chatgpt" || row.GetScheduledFor() != "2026-07-13" ||
		row.GetStatus() != opensightv1.RunStatus_RUN_STATUS_COMPLETED || row.GetTrigger() != opensightv1.RunTrigger_RUN_TRIGGER_SCHEDULED {
		t.Fatalf("run = %+v", row)
	}
	if row.CompletedAt == nil {
		t.Fatal("completed_at missing")
	}
	if runs.gotTenant.String() != tenantID || runs.gotBusiness != businessID {
		t.Fatalf("store called with tenant/business %s/%s", runs.gotTenant, runs.gotBusiness)
	}
}

// TestRPCListRunsPopulatesVisibility ports TestListRunsEndpointPopulatesVisibility:
// a run with a visibility point gets a pointer to the value, a run without
// one stays nil — not zero.
func TestRPCListRunsPopulatesVisibility(t *testing.T) {
	runID := mustHashV7(t, runIDForTest)
	businessID := mustHashV7(t, businessIDForTest)
	unanalyzedRunID := mustHashV7(t, promptIDForTest) // reuse a distinct valid v7 id
	runs := &fakeRunStore{runs: []store.RunListItem{
		{Run: store.Run{ID: runID, BusinessID: businessID, Platform: "chatgpt", Trigger: store.RunTriggerScheduled, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Status: store.RunStatusCompleted, WorkflowID: "run-a", StartedAt: time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC)}},
		{Run: store.Run{ID: unanalyzedRunID, BusinessID: businessID, Platform: "chatgpt", Trigger: store.RunTriggerScheduled, ScheduledFor: time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC), Status: store.RunStatusCompleted, WorkflowID: "run-b", StartedAt: time.Date(2026, 7, 6, 11, 0, 0, 0, time.UTC)}},
	}}
	m := &fakeRunsMetrics{trend: []metrics.VisibilityPoint{{RunID: runID, Percent: 42.5}}}
	srv := newResultRPCServer(runs, &fakeResultStore{}, m)

	resp, err := srv.ListRuns(businessRPCContext(t), connect.NewRequest(&opensightv1.ListRunsRequest{BusinessId: businessIDForTest}))
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	byID := map[string]*float64{}
	for _, r := range resp.Msg.GetRuns() {
		byID[r.GetId()] = r.Visibility
	}
	if v := byID[runID.String()]; v == nil || *v != 42.5 {
		t.Fatalf("run visibility = %v, want 42.5", v)
	}
	if v, ok := byID[unanalyzedRunID.String()]; !ok || v != nil {
		t.Fatalf("run with no visibility point = %v, want nil", v)
	}
}

// TestRPCListRunsNotFoundForCrossTenant confirms ListRuns is the ownership
// gate: its ErrNotFound becomes NotFound before VisibilityTrend runs (which
// does not error for an unowned business).
func TestRPCListRunsNotFoundForCrossTenant(t *testing.T) {
	runs := &fakeRunStore{err: store.ErrNotFound}
	srv := newResultRPCServer(runs, &fakeResultStore{}, &fakeRunsMetrics{})
	_, err := srv.ListRuns(businessRPCContext(t), connect.NewRequest(&opensightv1.ListRunsRequest{BusinessId: businessIDForTest}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

// TestRPCListRunsPopulatesNextRunAt is RUNS-2's best-effort contract: a
// successful schedule Describe populates next_run_at from
// Info.NextActionTimes[0], and a Describe error leaves it nil while the runs
// themselves still return normally (never s.rpcError, which would fail the
// whole request over a hint field).
func TestRPCListRunsPopulatesNextRunAt(t *testing.T) {
	businessID := mustHashV7(t, businessIDForTest)
	next := time.Date(2026, 7, 20, 2, 0, 0, 0, time.UTC)

	t.Run("populated from the schedule", func(t *testing.T) {
		temporal := &fakeTemporalClient{schedule: &fakeScheduleClient{nextActionAt: []time.Time{next}}}
		srv := &Server{runs: &fakeRunStore{}, results: &fakeResultStore{}, runMetrics: &fakeRunsMetrics{}, temporal: temporal}
		resp, err := srv.ListRuns(businessRPCContext(t), connect.NewRequest(&opensightv1.ListRunsRequest{BusinessId: businessIDForTest}))
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if resp.Msg.GetNextRunAt() == nil || !resp.Msg.GetNextRunAt().AsTime().Equal(next) {
			t.Fatalf("next_run_at = %v, want %v", resp.Msg.GetNextRunAt(), next)
		}
	})

	t.Run("describe error leaves it nil, runs still returned", func(t *testing.T) {
		temporal := &fakeTemporalClient{schedule: &fakeScheduleClient{describeErr: errors.New("temporal unreachable")}}
		runID := mustHashV7(t, runIDForTest)
		runs := &fakeRunStore{runs: []store.RunListItem{{Run: store.Run{ID: runID, BusinessID: businessID}}}}
		srv := &Server{runs: runs, results: &fakeResultStore{}, runMetrics: &fakeRunsMetrics{}, temporal: temporal}
		resp, err := srv.ListRuns(businessRPCContext(t), connect.NewRequest(&opensightv1.ListRunsRequest{BusinessId: businessIDForTest}))
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if resp.Msg.GetNextRunAt() != nil {
			t.Fatalf("next_run_at = %v, want nil on a describe error", resp.Msg.GetNextRunAt())
		}
		if len(resp.Msg.GetRuns()) != 1 {
			t.Fatalf("runs = %d, want 1 (describe error must not fail the request)", len(resp.Msg.GetRuns()))
		}
	})
}

// TestRPCListResultsParsesFiltersAndPagination ports
// TestListResultsEndpointParsesFiltersAndPagination.
func TestRPCListResultsParsesFiltersAndPagination(t *testing.T) {
	businessID := mustHashV7(t, businessIDForTest)
	runID := mustHashV7(t, runIDForTest)
	promptID := mustHashV7(t, promptIDForTest)
	resultID := mustHashV7(t, resultIDForTest)
	results := &fakeResultStore{results: []store.ResultListItem{{
		PromptResult: store.PromptResult{
			ID:          resultID,
			RunID:       runID,
			PromptID:    promptID,
			Status:      store.ResultStatusFailed,
			Request:     json.RawMessage(`{"model":"chat-latest"}`),
			Error:       ptrString("timeout"),
			RequestedAt: time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC),
			CompletedAt: time.Date(2026, 7, 13, 11, 1, 0, 0, time.UTC),
		},
		PromptText: "best clinic near me",
	}}}
	srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})

	resp, err := srv.ListResults(businessRPCContext(t), connect.NewRequest(&opensightv1.ListResultsRequest{
		BusinessId: businessIDForTest, RunId: runIDForTest, PromptId: promptIDForTest,
		Status: opensightv1.ResultStatus_RESULT_STATUS_FAILED, Limit: 25, Offset: 10,
	}))
	if err != nil {
		t.Fatalf("ListResults: %v", err)
	}
	if results.gotTenant.String() != tenantID || results.gotBusiness != businessID {
		t.Fatalf("store called with tenant/business %s/%s", results.gotTenant, results.gotBusiness)
	}
	if results.gotFilter.RunID == nil || *results.gotFilter.RunID != runID {
		t.Fatalf("run filter = %v, want %s", results.gotFilter.RunID, runID)
	}
	if results.gotFilter.PromptID == nil || *results.gotFilter.PromptID != promptID {
		t.Fatalf("prompt filter = %v, want %s", results.gotFilter.PromptID, promptID)
	}
	if results.gotFilter.Status == nil || *results.gotFilter.Status != store.ResultStatusFailed {
		t.Fatalf("status filter = %v, want failed", results.gotFilter.Status)
	}
	if results.gotFilter.Limit != 25 || results.gotFilter.Offset != 10 {
		t.Fatalf("paging filter = %d/%d, want 25/10", results.gotFilter.Limit, results.gotFilter.Offset)
	}

	body := resp.Msg
	if body.GetPaging().GetLimit() != 25 || body.GetPaging().GetOffset() != 10 || body.GetPaging().GetPageCount() != 1 {
		t.Fatalf("paging response = %+v", body.GetPaging())
	}
	got := body.GetResults()
	if len(got) != 1 || got[0].Error == nil || *got[0].Error != "timeout" {
		t.Fatalf("results response = %+v", got)
	}
	if got[0].GetPrompt() == nil || got[0].GetPrompt().GetText() != "best clinic near me" {
		t.Fatalf("list row prompt = %+v, want prompt text", got[0].GetPrompt())
	}
	if got[0].Run != nil || got[0].Analysis != nil {
		t.Fatalf("list row run/analysis = %+v/%+v, want both nil", got[0].Run, got[0].Analysis)
	}
}

// TestRPCListResultsParsesMentionedFilter has no full REST analogue for the
// nil case: REST's absent-vs-explicit-false wasn't cleanly representable in
// query params, but proto3's optional bool distinguishes all three states.
func TestRPCListResultsParsesMentionedFilter(t *testing.T) {
	trueVal, falseVal := true, false
	cases := []struct {
		name string
		req  *bool
	}{
		{"true", &trueVal},
		{"false", &falseVal},
		{"nil (absent)", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := &fakeResultStore{}
			srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})
			_, err := srv.ListResults(businessRPCContext(t), connect.NewRequest(&opensightv1.ListResultsRequest{
				BusinessId: businessIDForTest, Mentioned: tc.req,
			}))
			if err != nil {
				t.Fatalf("ListResults: %v", err)
			}
			if tc.req == nil {
				if results.gotFilter.Mentioned != nil {
					t.Fatalf("mentioned filter = %v, want nil", *results.gotFilter.Mentioned)
				}
			} else {
				if results.gotFilter.Mentioned == nil || *results.gotFilter.Mentioned != *tc.req {
					t.Fatalf("mentioned filter = %v, want %v", results.gotFilter.Mentioned, *tc.req)
				}
			}
		})
	}
}

// TestRPCListResultsRejectsInvalidFilters ports TestListResultsRejectsInvalidFilters,
// minus the limit/offset cases (rpcPaging normalizes those instead of erroring).
func TestRPCListResultsRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		name string
		req  *opensightv1.ListResultsRequest
	}{
		{"status", &opensightv1.ListResultsRequest{BusinessId: businessIDForTest, Status: opensightv1.ResultStatus(99)}},
		{"run uuid", &opensightv1.ListResultsRequest{BusinessId: businessIDForTest, RunId: "not-a-uuid"}},
		{"prompt uuid", &opensightv1.ListResultsRequest{BusinessId: businessIDForTest, PromptId: "not-a-uuid"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := &fakeResultStore{}
			srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})
			_, err := srv.ListResults(businessRPCContext(t), connect.NewRequest(tc.req))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
			}
			if results.listCalled != 0 {
				t.Fatalf("ListResults called %d times for invalid filter, want 0", results.listCalled)
			}
		})
	}
}

// TestRPCListResultsCapsPagination ports TestListResultsDefaultsAndCapsPagination,
// verifying the actual store.ResultFilter.Limit value, not just the response.
func TestRPCListResultsCapsPagination(t *testing.T) {
	results := &fakeResultStore{}
	srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})
	_, err := srv.ListResults(businessRPCContext(t), connect.NewRequest(&opensightv1.ListResultsRequest{
		BusinessId: businessIDForTest, Limit: 500,
	}))
	if err != nil {
		t.Fatalf("ListResults: %v", err)
	}
	if results.gotFilter.Limit != maxResultLimit || results.gotFilter.Offset != 0 {
		t.Fatalf("filter paging = %d/%d, want capped limit %d and offset 0", results.gotFilter.Limit, results.gotFilter.Offset, maxResultLimit)
	}
}

// TestRPCGetResultIncludesRawOnDemand ports TestGetResultEndpointIncludesRawOnDemand.
func TestRPCGetResultIncludesRawOnDemand(t *testing.T) {
	runID := mustHashV7(t, runIDForTest)
	businessID := mustHashV7(t, businessIDForTest)
	promptID := mustHashV7(t, promptIDForTest)
	resultID := mustHashV7(t, resultIDForTest)
	results := &fakeResultStore{detail: store.ResultDetail{
		Result: store.PromptResult{
			ID:           resultID,
			RunID:        runID,
			PromptID:     promptID,
			Status:       store.ResultStatusSucceeded,
			Model:        ptrString("gpt-5-mini-2026-07-01"),
			Request:      json.RawMessage(`{"model":"chat-latest"}`),
			RawResponse:  json.RawMessage(`{"id":"resp_1"}`),
			ResponseText: ptrString("Acme Clinic is recommended."),
			RequestedAt:  time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC),
			CompletedAt:  time.Date(2026, 7, 13, 11, 1, 0, 0, time.UTC),
		},
		Prompt: store.Prompt{ID: promptID, BusinessID: businessID, Text: "best clinic near me"},
		Run: store.Run{
			ID:           runID,
			BusinessID:   businessID,
			Platform:     "chatgpt",
			Trigger:      store.RunTriggerScheduled,
			ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
			Status:       store.RunStatusCompleted,
			WorkflowID:   "run-" + runIDForTest,
			StartedAt:    time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC),
		},
		BusinessID: businessID,
	}}
	srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})

	noRaw, err := srv.GetResult(businessRPCContext(t), connect.NewRequest(&opensightv1.GetResultRequest{ResultId: resultIDForTest}))
	if err != nil {
		t.Fatalf("GetResult (no raw): %v", err)
	}
	if noRaw.Msg.GetResult().GetRawResponseJson() != "" {
		t.Fatalf("raw_response_json = %q, want empty when include_raw is false", noRaw.Msg.GetResult().GetRawResponseJson())
	}

	withRaw, err := srv.GetResult(businessRPCContext(t), connect.NewRequest(&opensightv1.GetResultRequest{ResultId: resultIDForTest, IncludeRaw: true}))
	if err != nil {
		t.Fatalf("GetResult (with raw): %v", err)
	}
	got := withRaw.Msg.GetResult()
	if got.GetRawResponseJson() != `{"id":"resp_1"}` {
		t.Fatalf("raw_response_json = %s, want resp_1 JSON", got.GetRawResponseJson())
	}
	if got.GetPrompt() == nil || got.GetPrompt().GetText() != "best clinic near me" {
		t.Fatalf("prompt = %+v", got.GetPrompt())
	}
	if got.GetRun() == nil || got.GetRun().GetScheduledFor() != "2026-07-13" {
		t.Fatalf("run = %+v", got.GetRun())
	}
	if got.GetRun().Visibility != nil {
		t.Fatalf("run.visibility = %v, want nil on the detail view", got.GetRun().Visibility)
	}
}

// TestRPCGetResultIncludesAnalysisWithReconstructedSpans ports
// TestGetResultEndpointIncludesAnalysisWithReconstructedSpans — the most
// important test in this story. Two url_citation annotations are supplied in
// an order different from their array/cite_order, proving citationSpansToProto
// sorts by StartIndex before cite_order indexes into it.
func TestRPCGetResultIncludesAnalysisWithReconstructedSpans(t *testing.T) {
	runID := mustHashV7(t, runIDForTest)
	businessID := mustHashV7(t, businessIDForTest)
	promptID := mustHashV7(t, promptIDForTest)
	resultID := mustHashV7(t, resultIDForTest)

	// First array element starts at 40, the second at 10. citationSpansToProto
	// sorts by StartIndex, so span[0] must be the [10,20] annotation and
	// span[1] the [40,50] one — and cite_order indexes into that sorted list.
	raw := json.RawMessage(`{"output":[{"type":"message","content":[{"type":"output_text","annotations":[` +
		`{"type":"url_citation","url":"https://a.example.com","title":"A","start_index":40,"end_index":50},` +
		`{"type":"url_citation","url":"https://b.example.com","title":"B","start_index":10,"end_index":20}` +
		`]}]}]}`)

	results := &fakeResultStore{
		detail: store.ResultDetail{
			Result: store.PromptResult{
				ID:           resultID,
				RunID:        runID,
				PromptID:     promptID,
				Status:       store.ResultStatusSucceeded,
				Model:        ptrString("gpt-5-mini"),
				Request:      json.RawMessage(`{"model":"chat-latest"}`),
				RawResponse:  raw,
				ResponseText: ptrString("B then A are cited."),
				RequestedAt:  time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC),
				CompletedAt:  time.Date(2026, 7, 13, 11, 1, 0, 0, time.UTC),
			},
			Prompt:     store.Prompt{ID: promptID, BusinessID: businessID, Text: "best clinic near me"},
			Run:        store.Run{ID: runID, BusinessID: businessID, Platform: "chatgpt", Trigger: store.RunTriggerScheduled, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Status: store.RunStatusCompleted, WorkflowID: "run-x", StartedAt: time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC)},
			BusinessID: businessID,
		},
		analysis: store.ResultAnalysis{
			Analyzed:  true,
			Sentiment: ptrString("positive"),
			Keywords:  []string{"friendly", "affordable"},
			Excerpts:  []string{"Clinic is a good option."},
			Mentions: []store.ResultMention{
				{Subject: "self", VerbatimName: "Clinic", MatchedBy: "exact", MentionOrder: 0, Excerpt: "Clinic ... option."},
			},
			// cite_order 0 belongs to the B annotation (start_index 10), 1 to A (40).
			Citations: []store.ResultCitation{
				{URL: "https://b.example.com", Domain: "b.example.com", Title: ptrString("B"), CiteOrder: 0, Subject: "business"},
				{URL: "https://a.example.com", Domain: "a.example.com", Title: ptrString("A"), CiteOrder: 1, Subject: "other"},
			},
		},
	}
	srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})

	resp, err := srv.GetResult(businessRPCContext(t), connect.NewRequest(&opensightv1.GetResultRequest{ResultId: resultIDForTest}))
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	body := resp.Msg.GetResult()
	if body.GetUnanalyzed() {
		t.Fatal("unanalyzed = true, want false for an analyzed result")
	}
	analysis := body.GetAnalysis()
	if analysis == nil {
		t.Fatal("analysis field missing on an analyzed result")
	}
	if analysis.GetSentiment() != opensightv1.Sentiment_SENTIMENT_POSITIVE {
		t.Fatalf("sentiment = %v, want POSITIVE", analysis.GetSentiment())
	}
	if len(analysis.GetKeywords()) != 2 || analysis.GetKeywords()[0] != "friendly" {
		t.Fatalf("keywords = %v, want [friendly affordable]", analysis.GetKeywords())
	}
	mentions := analysis.GetMentions()
	if len(mentions) != 1 || mentions[0].GetSubject() != opensightv1.MentionSubject_MENTION_SUBJECT_SELF ||
		mentions[0].GetVerbatimName() != "Clinic" || mentions[0].GetOrder() != 0 || mentions[0].GetMatchedBy() != opensightv1.MatchMethod_MATCH_METHOD_EXACT {
		t.Fatalf("mentions = %+v, want one self/exact mention at order 0", mentions)
	}
	citations := analysis.GetCitations()
	if len(citations) != 2 {
		t.Fatalf("citations = %d, want 2", len(citations))
	}
	// cite_order 0 (b.example.com) must resolve to the StartIndex-sorted span [10,20].
	c0 := citations[0]
	if c0.GetDomain() != "b.example.com" || c0.GetSpan() == nil || c0.GetSpan().GetStart() != 10 || c0.GetSpan().GetEnd() != 20 || c0.GetSubject() != opensightv1.CitationSubject_CITATION_SUBJECT_BUSINESS {
		t.Fatalf("citation[0] = %+v, want b.example.com span [10,20] subject BUSINESS", c0)
	}
	// cite_order 1 (a.example.com) resolves to span [40,50] — array-order would
	// have (incorrectly) given it [10,20].
	c1 := citations[1]
	if c1.GetDomain() != "a.example.com" || c1.GetSpan() == nil || c1.GetSpan().GetStart() != 40 || c1.GetSpan().GetEnd() != 50 || c1.GetSubject() != opensightv1.CitationSubject_CITATION_SUBJECT_OTHER {
		t.Fatalf("citation[1] = %+v, want a.example.com span [40,50] subject OTHER", c1)
	}
}

// TestRPCGetResultUnanalyzedAndFailedFlags ports TestGetResultEndpointUnanalyzedAndFailedFlags.
func TestRPCGetResultUnanalyzedAndFailedFlags(t *testing.T) {
	runID := mustHashV7(t, runIDForTest)
	businessID := mustHashV7(t, businessIDForTest)
	promptID := mustHashV7(t, promptIDForTest)
	resultID := mustHashV7(t, resultIDForTest)
	baseRun := store.Run{ID: runID, BusinessID: businessID, Platform: "chatgpt", Trigger: store.RunTriggerScheduled, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Status: store.RunStatusCompleted, WorkflowID: "run-x", StartedAt: time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC)}

	t.Run("succeeded but unanalyzed", func(t *testing.T) {
		results := &fakeResultStore{
			detail: store.ResultDetail{
				Result:     store.PromptResult{ID: resultID, RunID: runID, PromptID: promptID, Status: store.ResultStatusSucceeded, Request: json.RawMessage(`{}`), ResponseText: ptrString("ok"), RequestedAt: baseRun.StartedAt, CompletedAt: baseRun.StartedAt},
				Prompt:     store.Prompt{ID: promptID, BusinessID: businessID, Text: "q"},
				Run:        baseRun,
				BusinessID: businessID,
			},
			analysis: store.ResultAnalysis{Analyzed: false},
		}
		srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})
		resp, err := srv.GetResult(businessRPCContext(t), connect.NewRequest(&opensightv1.GetResultRequest{ResultId: resultIDForTest}))
		if err != nil {
			t.Fatalf("GetResult: %v", err)
		}
		body := resp.Msg.GetResult()
		if !body.GetUnanalyzed() {
			t.Fatal("unanalyzed = false, want true")
		}
		if body.Analysis != nil {
			t.Fatalf("analysis present on unanalyzed result: %+v", body.Analysis)
		}
	})

	t.Run("failed", func(t *testing.T) {
		results := &fakeResultStore{
			detail: store.ResultDetail{
				Result:     store.PromptResult{ID: resultID, RunID: runID, PromptID: promptID, Status: store.ResultStatusFailed, Request: json.RawMessage(`{}`), Error: ptrString("timeout"), RequestedAt: baseRun.StartedAt, CompletedAt: baseRun.StartedAt},
				Prompt:     store.Prompt{ID: promptID, BusinessID: businessID, Text: "q"},
				Run:        baseRun,
				BusinessID: businessID,
			},
			analysis: store.ResultAnalysis{Analyzed: false},
		}
		srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})
		resp, err := srv.GetResult(businessRPCContext(t), connect.NewRequest(&opensightv1.GetResultRequest{ResultId: resultIDForTest}))
		if err != nil {
			t.Fatalf("GetResult: %v", err)
		}
		body := resp.Msg.GetResult()
		if body.GetUnanalyzed() {
			t.Fatal("unanalyzed = true for a failed result, want false")
		}
		if body.Analysis != nil {
			t.Fatalf("analysis present on a failed result: %+v", body.Analysis)
		}
	})
}

// TestRPCGetResultNotFoundForCrossTenant ports
// TestResponsesEndpointsReturnNotFoundForCrossTenantRows.
func TestRPCGetResultNotFoundForCrossTenant(t *testing.T) {
	results := &fakeResultStore{detailErr: store.ErrNotFound}
	srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})
	_, err := srv.GetResult(businessRPCContext(t), connect.NewRequest(&opensightv1.GetResultRequest{ResultId: resultIDForTest}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

// TestRPCGetResultInternalError ports TestResponsesEndpointsReturnInternalErrors,
// and confirms the client-visible message is the fixed generic one, not the
// underlying error string (the no-oracle property).
func TestRPCGetResultInternalError(t *testing.T) {
	results := &fakeResultStore{detailErr: errors.New("db down")}
	srv := newResultRPCServer(&fakeRunStore{}, results, &fakeRunsMetrics{})
	_, err := srv.GetResult(businessRPCContext(t), connect.NewRequest(&opensightv1.GetResultRequest{ResultId: resultIDForTest}))
	cerr := asConnectError(t, err)
	if cerr.Code() != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", cerr.Code())
	}
	if cerr.Message() != "an unexpected error occurred" {
		t.Fatalf("message = %q, want the fixed generic message (not %q)", cerr.Message(), "db down")
	}
}
