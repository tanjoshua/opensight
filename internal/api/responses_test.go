package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/metrics"
	"opensight/internal/store"
)

type fakeRunStore struct {
	runs        []store.Run
	err         error
	gotTenant   domain.ID
	gotBusiness domain.ID
	called      int
}

func (f *fakeRunStore) ListRuns(_ context.Context, tenantID, businessID domain.ID) ([]store.Run, error) {
	f.called++
	f.gotTenant = tenantID
	f.gotBusiness = businessID
	return f.runs, f.err
}

type fakeResultStore struct {
	results        []store.ResultListItem
	detail         store.ResultDetail
	analysis       store.ResultAnalysis
	listErr        error
	detailErr      error
	analysisErr    error
	gotTenant      domain.ID
	gotBusiness    domain.ID
	gotResult      domain.ID
	gotFilter      store.ResultFilter
	listCalled     int
	detailCalled   int
	analysisCalled int
}

func (f *fakeResultStore) ListResults(_ context.Context, tenantID, businessID domain.ID, filter store.ResultFilter) ([]store.ResultListItem, error) {
	f.listCalled++
	f.gotTenant = tenantID
	f.gotBusiness = businessID
	f.gotFilter = filter
	return f.results, f.listErr
}

func (f *fakeResultStore) GetResultDetail(_ context.Context, tenantID, resultID domain.ID) (store.ResultDetail, error) {
	f.detailCalled++
	f.gotTenant = tenantID
	f.gotResult = resultID
	return f.detail, f.detailErr
}

func (f *fakeResultStore) GetResultAnalysis(_ context.Context, tenantID, resultID domain.ID) (store.ResultAnalysis, error) {
	f.analysisCalled++
	f.gotTenant = tenantID
	f.gotResult = resultID
	return f.analysis, f.analysisErr
}

type fakeRunsMetrics struct {
	trend []metrics.VisibilityPoint
	err   error
}

func (f *fakeRunsMetrics) VisibilityTrend(context.Context, domain.ID, domain.ID) ([]metrics.VisibilityPoint, error) {
	return f.trend, f.err
}

func newAuthedResponseServer(t *testing.T, runs runStore, results resultStore) (*Server, *http.Cookie) {
	t.Helper()
	raw := "response-test-session"
	f := &fakeAuthStore{
		sessions: map[string]store.SessionUser{
			string(hashSessionToken(raw)): {
				UserID:     mustHashV7(t, userID),
				TenantID:   mustHashV7(t, tenantID),
				Email:      "user@example.com",
				TenantName: "Acme Clinic",
				ExpiresAt:  time.Now().Add(time.Hour),
			},
		},
	}
	return &Server{auth: f, runs: runs, results: results, runMetrics: &fakeRunsMetrics{}, secureCookies: false, sessionTTL: time.Hour},
		&http.Cookie{Name: sessionCookieName, Value: raw}
}

func TestListRunsEndpoint(t *testing.T) {
	runID := mustHashV7(t, runIDForTest)
	businessID := mustHashV7(t, businessIDForTest)
	completedAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	runs := &fakeRunStore{runs: []store.Run{{
		ID:           runID,
		BusinessID:   businessID,
		Platform:     "chatgpt",
		Trigger:      store.RunTriggerScheduled,
		ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
		Status:       store.RunStatusCompleted,
		WorkflowID:   "run-" + runIDForTest,
		StartedAt:    time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC),
		CompletedAt:  &completedAt,
	}}}
	srv, cookie := newAuthedResponseServer(t, runs, &fakeResultStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/businesses/"+businessIDForTest+"/runs", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if runs.gotTenant.String() != tenantID || runs.gotBusiness != businessID {
		t.Fatalf("store called with tenant/business %s/%s", runs.gotTenant, runs.gotBusiness)
	}

	var body runsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Runs) != 1 || body.Runs[0].ScheduledFor != "2026-07-13" || body.Runs[0].Status != "completed" {
		t.Fatalf("runs response = %+v", body.Runs)
	}
}

func TestListResultsEndpointParsesFiltersAndPagination(t *testing.T) {
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
	srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/businesses/"+businessIDForTest+"/results?run="+runIDForTest+"&prompt="+promptIDForTest+"&status=failed&limit=25&offset=10",
		nil,
	)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
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

	var body resultsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Paging.Limit != 25 || body.Paging.Offset != 10 || body.Paging.PageCount != 1 {
		t.Fatalf("paging response = %+v", body.Paging)
	}
	if len(body.Results) != 1 || body.Results[0].Error == nil || *body.Results[0].Error != "timeout" {
		t.Fatalf("results response = %+v", body.Results)
	}
	if body.Results[0].Prompt == nil || body.Results[0].Prompt.Text != "best clinic near me" {
		t.Fatalf("list row prompt = %+v, want prompt text", body.Results[0].Prompt)
	}
}

func TestGetResultEndpointIncludesRawOnDemand(t *testing.T) {
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
	srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)

	noRaw := doAuthedGET(t, srv, cookie, "/api/v1/results/"+resultIDForTest)
	if noRaw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", noRaw.Code, noRaw.Body.String())
	}
	var noRawMap map[string]any
	if err := json.Unmarshal(noRaw.Body.Bytes(), &noRawMap); err != nil {
		t.Fatalf("decode no-raw response: %v", err)
	}
	if _, ok := noRawMap["raw_response"]; ok {
		t.Fatalf("raw_response was included without include_raw=true: %s", noRaw.Body.String())
	}

	withRaw := doAuthedGET(t, srv, cookie, "/api/v1/results/"+resultIDForTest+"?include_raw=true")
	if withRaw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", withRaw.Code, withRaw.Body.String())
	}
	var body resultResponse
	if err := json.NewDecoder(withRaw.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RawResponse == nil || string(body.RawResponse) != `{"id":"resp_1"}` {
		t.Fatalf("raw response = %s, want resp_1 JSON", string(body.RawResponse))
	}
	if body.Prompt == nil || body.Prompt.Text != "best clinic near me" {
		t.Fatalf("prompt response = %+v", body.Prompt)
	}
	if body.Run == nil || body.Run.ScheduledFor != "2026-07-13" {
		t.Fatalf("run response = %+v", body.Run)
	}
}

func TestGetResultEndpointIncludesAnalysisWithReconstructedSpans(t *testing.T) {
	runID := mustHashV7(t, runIDForTest)
	businessID := mustHashV7(t, businessIDForTest)
	promptID := mustHashV7(t, promptIDForTest)
	resultID := mustHashV7(t, resultIDForTest)

	// Two url_citation annotations in array order that is NOT StartIndex order:
	// the first array element starts at 40, the second at 10. citationSpans sorts
	// by StartIndex, so span[0] must be the [10,20] annotation and span[1] the
	// [40,50] one — and cite_order indexes into that sorted list.
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
				{Subject: "self", MatchedBy: "exact", MentionOrder: 0, Excerpt: "Clinic ... option."},
			},
			// cite_order 0 belongs to the B annotation (start_index 10), 1 to A (40).
			Citations: []store.ResultCitation{
				{URL: "https://b.example.com", Domain: "b.example.com", Title: ptrString("B"), CiteOrder: 0, Subject: "business"},
				{URL: "https://a.example.com", Domain: "a.example.com", Title: ptrString("A"), CiteOrder: 1, Subject: "other"},
			},
		},
	}
	srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)

	rec := doAuthedGET(t, srv, cookie, "/api/v1/results/"+resultIDForTest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body resultResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Unanalyzed {
		t.Fatal("unanalyzed = true, want false for an analyzed result")
	}
	if body.Analysis == nil {
		t.Fatal("analysis field missing on an analyzed result")
	}
	if body.Analysis.Sentiment == nil || *body.Analysis.Sentiment != "positive" {
		t.Fatalf("sentiment = %v, want positive", body.Analysis.Sentiment)
	}
	if len(body.Analysis.Keywords) != 2 || body.Analysis.Keywords[0] != "friendly" {
		t.Fatalf("keywords = %v, want [friendly affordable]", body.Analysis.Keywords)
	}
	if len(body.Analysis.Mentions) != 1 || body.Analysis.Mentions[0].Subject != "self" || body.Analysis.Mentions[0].Order != 0 {
		t.Fatalf("mentions = %+v, want one self mention at order 0", body.Analysis.Mentions)
	}
	if len(body.Analysis.Citations) != 2 {
		t.Fatalf("citations = %d, want 2", len(body.Analysis.Citations))
	}
	// cite_order 0 (b.example.com) must resolve to the StartIndex-sorted span [10,20].
	c0 := body.Analysis.Citations[0]
	if c0.Domain != "b.example.com" || c0.Span == nil || c0.Span.Start != 10 || c0.Span.End != 20 {
		t.Fatalf("citation[0] = %+v, want b.example.com span [10,20]", c0)
	}
	// cite_order 1 (a.example.com) resolves to span [40,50] — array-order would have
	// (incorrectly) given it [10,20].
	c1 := body.Analysis.Citations[1]
	if c1.Domain != "a.example.com" || c1.Span == nil || c1.Span.Start != 40 || c1.Span.End != 50 {
		t.Fatalf("citation[1] = %+v, want a.example.com span [40,50]", c1)
	}
}

func TestGetResultEndpointUnanalyzedAndFailedFlags(t *testing.T) {
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
		srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)
		rec := doAuthedGET(t, srv, cookie, "/api/v1/results/"+resultIDForTest)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if m["unanalyzed"] != true {
			t.Fatalf("unanalyzed = %v, want true", m["unanalyzed"])
		}
		if _, ok := m["analysis"]; ok {
			t.Fatalf("analysis field present on unanalyzed result: %s", rec.Body.String())
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
		srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)
		rec := doAuthedGET(t, srv, cookie, "/api/v1/results/"+resultIDForTest)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var body resultResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Unanalyzed {
			t.Fatal("unanalyzed = true for a failed result, want false")
		}
		if body.Analysis != nil {
			t.Fatal("analysis field present on a failed result")
		}
	})
}

func TestListResultsEndpointParsesMentionedFilter(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: "true", want: true},
		{name: "false", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := &fakeResultStore{}
			srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)
			rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/results?mentioned="+tc.name)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if results.gotFilter.Mentioned == nil || *results.gotFilter.Mentioned != tc.want {
				t.Fatalf("mentioned filter = %v, want %v", results.gotFilter.Mentioned, tc.want)
			}
		})
	}
}

func TestListRunsEndpointPopulatesVisibility(t *testing.T) {
	runID := mustHashV7(t, runIDForTest)
	businessID := mustHashV7(t, businessIDForTest)
	unanalyzedRunID := mustHashV7(t, promptIDForTest) // reuse a distinct valid v7 id
	runs := &fakeRunStore{runs: []store.Run{
		{ID: runID, BusinessID: businessID, Platform: "chatgpt", Trigger: store.RunTriggerScheduled, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Status: store.RunStatusCompleted, WorkflowID: "run-a", StartedAt: time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC)},
		{ID: unanalyzedRunID, BusinessID: businessID, Platform: "chatgpt", Trigger: store.RunTriggerScheduled, ScheduledFor: time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC), Status: store.RunStatusCompleted, WorkflowID: "run-b", StartedAt: time.Date(2026, 7, 6, 11, 0, 0, 0, time.UTC)},
	}}
	srv, cookie := newAuthedResponseServer(t, runs, &fakeResultStore{})
	// Only the first run has a visibility point; the second has none.
	srv.runMetrics = &fakeRunsMetrics{trend: []metrics.VisibilityPoint{{RunID: runID, Percent: 42.5}}}

	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/runs")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body runsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byID := map[string]*float64{}
	for _, r := range body.Runs {
		byID[r.ID] = r.Visibility
	}
	if v := byID[runID.String()]; v == nil || *v != 42.5 {
		t.Fatalf("run visibility = %v, want 42.5", v)
	}
	if v, ok := byID[unanalyzedRunID.String()]; !ok || v != nil {
		t.Fatalf("run with no visibility point = %v, want null", v)
	}
}

func TestResponsesEndpointsRequireAuth(t *testing.T) {
	srv := &Server{auth: &fakeAuthStore{}, runs: &fakeRunStore{}, results: &fakeResultStore{}, secureCookies: false, sessionTTL: time.Hour}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/businesses/"+businessIDForTest+"/runs", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListResultsRejectsInvalidFilters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "status", query: "status=bogus"},
		{name: "limit", query: "limit=0"},
		{name: "offset", query: "offset=-1"},
		{name: "run uuid", query: "run=not-a-uuid"},
		{name: "prompt uuid", query: "prompt=not-a-uuid"},
		{name: "mentioned", query: "mentioned=maybe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := &fakeResultStore{}
			srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)
			rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/results?"+tc.query)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if results.listCalled != 0 {
				t.Fatalf("ListResults called %d times for invalid query, want 0", results.listCalled)
			}
		})
	}
}

func TestListResultsDefaultsAndCapsPagination(t *testing.T) {
	results := &fakeResultStore{}
	srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)
	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/results?limit=500")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if results.gotFilter.Limit != maxResultLimit || results.gotFilter.Offset != 0 {
		t.Fatalf("filter paging = %d/%d, want capped limit %d and offset 0", results.gotFilter.Limit, results.gotFilter.Offset, maxResultLimit)
	}
}

func TestResponsesEndpointsReturnNotFoundForCrossTenantRows(t *testing.T) {
	results := &fakeResultStore{detailErr: store.ErrNotFound}
	srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)
	rec := doAuthedGET(t, srv, cookie, "/api/v1/results/"+resultIDForTest)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestResponsesEndpointsReturnInternalErrors(t *testing.T) {
	results := &fakeResultStore{detailErr: errors.New("db down")}
	srv, cookie := newAuthedResponseServer(t, &fakeRunStore{}, results)
	rec := doAuthedGET(t, srv, cookie, "/api/v1/results/"+resultIDForTest)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

func doAuthedGET(t *testing.T, srv *Server, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

const (
	businessIDForTest = "01950000-0000-7000-8000-0000000000c3"
	runIDForTest      = "01950000-0000-7000-8000-0000000000d4"
	promptIDForTest   = "01950000-0000-7000-8000-0000000000e5"
	resultIDForTest   = "01950000-0000-7000-8000-0000000000f6"
)
