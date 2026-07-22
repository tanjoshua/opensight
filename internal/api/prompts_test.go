package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/metrics"
	"opensight/internal/store"
)

type fakePromptStore struct {
	active     []store.Prompt
	byID       map[domain.ID]store.Prompt
	listErr    error
	getErr     error
	listCalled int
}

func (f *fakePromptStore) ListActivePrompts(_ context.Context, _, _ domain.ID) ([]store.Prompt, error) {
	f.listCalled++
	return f.active, f.listErr
}

func (f *fakePromptStore) GetPrompt(_ context.Context, _, promptID domain.ID) (store.Prompt, error) {
	if f.getErr != nil {
		return store.Prompt{}, f.getErr
	}
	p, ok := f.byID[promptID]
	if !ok {
		return store.Prompt{}, store.ErrNotFound
	}
	return p, nil
}

type fakePromptsMetrics struct {
	latest []metrics.PromptLatest
	trends map[domain.ID][]metrics.PromptTrendPoint
	err    error
}

func (f *fakePromptsMetrics) PromptLatestStats(context.Context, domain.ID, domain.ID) ([]metrics.PromptLatest, error) {
	return f.latest, f.err
}
func (f *fakePromptsMetrics) PromptTrends(context.Context, domain.ID, domain.ID) (map[domain.ID][]metrics.PromptTrendPoint, error) {
	return f.trends, f.err
}

func newAuthedPromptsServer(t *testing.T, prompts promptStore, results resultStore, m promptsMetrics) (*Server, *http.Cookie) {
	t.Helper()
	raw := "prompts-test-session"
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
	return &Server{auth: f, prompts: prompts, results: results, promptMetrics: m, secureCookies: false, sessionTTL: time.Hour},
		&http.Cookie{Name: sessionCookieName, Value: raw}
}

// TestListPromptsShapesSummaryAndTrend pins the join: an analyzed prompt carries
// its latest summary + result_id door and its spark-trend; an active prompt with
// no analyzed result yet reports the honest "not measured" state (null
// latest_result_id, empty trend), not a false absence.
func TestListPromptsShapesSummaryAndTrend(t *testing.T) {
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

	srv, cookie := newAuthedPromptsServer(t, prompts, &fakeResultStore{}, m)
	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/prompts")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var body promptsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Prompts) != 2 {
		t.Fatalf("prompts = %d, want 2", len(body.Prompts))
	}

	got := body.Prompts[0]
	if got.ID != analyzed.String() || !got.Mentioned || got.Order == nil || *got.Order != 0 || got.Sentiment == nil || *got.Sentiment != "positive" {
		t.Fatalf("analyzed prompt summary = %+v", got)
	}
	if got.LatestResultID == nil || *got.LatestResultID != resultID.String() {
		t.Fatalf("analyzed prompt latest_result_id = %v, want door to %s", got.LatestResultID, resultID)
	}
	if len(got.Trend) != 1 || got.Trend[0].ScheduledFor != "2026-07-13" || !got.Trend[0].Mentioned || got.Trend[0].ResultID != resultID.String() {
		t.Fatalf("analyzed prompt trend = %+v", got.Trend)
	}

	fr := body.Prompts[1]
	if fr.ID != fresh.String() || fr.Mentioned || fr.Order != nil || fr.Sentiment != nil || fr.LatestResultID != nil || len(fr.Trend) != 0 {
		t.Fatalf("unanalyzed prompt summary = %+v, want not-measured state", fr)
	}
}

// TestListPromptsNotFoundForCrossTenant confirms ListActivePrompts is the
// ownership gate: its ErrNotFound becomes a 404 before any metric runs.
func TestListPromptsNotFoundForCrossTenant(t *testing.T) {
	prompts := &fakePromptStore{listErr: store.ErrNotFound}
	m := &fakePromptsMetrics{}
	srv, cookie := newAuthedPromptsServer(t, prompts, &fakeResultStore{}, m)
	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/prompts")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestGetPromptDetailWalksLineageAndHistory pins GET /prompts/:id: the retired
// prompt is reachable, its full result history is returned, and its lineage chain
// is walked back through replaces_prompt_id.
func TestGetPromptDetailWalksLineageAndHistory(t *testing.T) {
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

	srv, cookie := newAuthedPromptsServer(t, prompts, results, &fakePromptsMetrics{})
	rec := doAuthedGET(t, srv, cookie, "/api/v1/prompts/"+promptIDForTest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var body promptDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Prompt.ID != current.String() || body.Prompt.ReplacesPromptID == nil || *body.Prompt.ReplacesPromptID != middle.String() {
		t.Fatalf("prompt node = %+v", body.Prompt)
	}
	if len(body.Lineage) != 2 || body.Lineage[0].ID != middle.String() || body.Lineage[1].ID != oldest.String() {
		t.Fatalf("lineage = %+v, want middle then oldest", body.Lineage)
	}
	if body.Lineage[1].ReplacesPromptID != nil {
		t.Fatalf("oldest node replaces = %v, want nil", body.Lineage[1].ReplacesPromptID)
	}
	if len(body.Results) != 1 || body.Results[0].ID != resultID.String() {
		t.Fatalf("results = %+v", body.Results)
	}
	if results.gotFilter.PromptID == nil || *results.gotFilter.PromptID != current {
		t.Fatalf("result filter prompt = %v, want %s", results.gotFilter.PromptID, current)
	}
	if results.gotBusiness != businessID {
		t.Fatalf("results scoped to business %s, want %s", results.gotBusiness, businessID)
	}
}

// TestGetPromptNotFoundForCrossTenant confirms GetPrompt is the tenant gate:
// its ErrNotFound becomes a 404 before any history or lineage is fetched.
func TestGetPromptNotFoundForCrossTenant(t *testing.T) {
	prompts := &fakePromptStore{getErr: store.ErrNotFound}
	results := &fakeResultStore{}
	srv, cookie := newAuthedPromptsServer(t, prompts, results, &fakePromptsMetrics{})
	rec := doAuthedGET(t, srv, cookie, "/api/v1/prompts/"+promptIDForTest)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if results.listCalled != 0 {
		t.Fatalf("ListResults called %d times before ownership proven, want 0", results.listCalled)
	}
}
