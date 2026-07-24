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

type fakeOverviewMetrics struct {
	trend       []metrics.VisibilityPoint
	keywords    []metrics.KeywordStat
	domains     []metrics.DomainStat
	competitors metrics.CompetitorStats
	changes     []metrics.PromptChange
	err         error
}

func (f *fakeOverviewMetrics) VisibilityTrend(context.Context, domain.ID, domain.ID) ([]metrics.VisibilityPoint, error) {
	return f.trend, f.err
}
func (f *fakeOverviewMetrics) KeywordStats(context.Context, domain.ID, domain.ID) ([]metrics.KeywordStat, error) {
	return f.keywords, f.err
}
func (f *fakeOverviewMetrics) CitationDomainStats(context.Context, domain.ID, domain.ID) ([]metrics.DomainStat, error) {
	return f.domains, f.err
}
func (f *fakeOverviewMetrics) CompetitorStats(context.Context, domain.ID, domain.ID) (metrics.CompetitorStats, error) {
	return f.competitors, f.err
}
func (f *fakeOverviewMetrics) PromptChanges(context.Context, domain.ID, domain.ID) ([]metrics.PromptChange, error) {
	return f.changes, f.err
}

func newAuthedOverviewServer(t *testing.T, runs runStore, m overviewMetrics) (*Server, *http.Cookie) {
	t.Helper()
	raw := "overview-test-session"
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
	return &Server{auth: f, runs: runs, metrics: m, secureCookies: false, sessionTTL: time.Hour},
		&http.Cookie{Name: sessionCookieName, Value: raw}
}

// TestOverviewEndpointShapesPayload pins the handler-only logic: current/delta
// derived from the trend, top-N truncation, the tracked + top-3-discovered
// competitor filter (dismissed dropped), latest run, and result_ids carried
// through.
func TestOverviewEndpointShapesPayload(t *testing.T) {
	resultID := mustHashV7(t, resultIDForTest)
	runID := mustHashV7(t, runIDForTest)
	businessID := mustHashV7(t, businessIDForTest)

	keywords := make([]metrics.KeywordStat, 0, 6)
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		keywords = append(keywords, metrics.KeywordStat{Keyword: k, ResultIDs: []domain.ID{resultID}})
	}

	m := &fakeOverviewMetrics{
		trend: []metrics.VisibilityPoint{
			{RunID: runID, ScheduledFor: time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC), Analyzed: 20, Mentioned: 8, Percent: 40, ResultIDs: []domain.ID{resultID}},
			{RunID: runID, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Analyzed: 20, Mentioned: 11, Percent: 55, ResultIDs: []domain.ID{resultID}},
		},
		keywords: keywords,
		domains:  []metrics.DomainStat{{Domain: "healthline.com", ResultIDs: []domain.ID{resultID}}},
		competitors: metrics.CompetitorStats{
			SelfPercent: 55,
			Competitors: []metrics.CompetitorStat{
				{CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000101"), Name: "Tracked Co", Status: "tracked", MentionPercent: 30, VsSelf: -25, ResultIDs: []domain.ID{resultID}},
				{CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000102"), Name: "Disc 1", Status: "discovered", MentionPercent: 25, ResultIDs: []domain.ID{resultID}},
				{CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000103"), Name: "Disc 2", Status: "discovered", MentionPercent: 20, ResultIDs: []domain.ID{resultID}},
				{CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000104"), Name: "Disc 3", Status: "discovered", MentionPercent: 15, ResultIDs: []domain.ID{resultID}},
				{CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000105"), Name: "Disc 4", Status: "discovered", MentionPercent: 10, ResultIDs: []domain.ID{resultID}},
				{CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000106"), Name: "Dismissed Co", Status: "dismissed", MentionPercent: 50, ResultIDs: []domain.ID{resultID}},
			},
		},
		changes: []metrics.PromptChange{
			{Date: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), Added: 2},
			{Date: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Replaced: 1},
		},
	}
	runs := &fakeRunStore{runs: []store.Run{{
		ID: runID, BusinessID: businessID, Platform: "chatgpt", Trigger: store.RunTriggerScheduled,
		ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Status: store.RunStatusCompleted,
		WorkflowID: "run-" + runIDForTest, StartedAt: time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC),
	}}}

	srv, cookie := newAuthedOverviewServer(t, runs, m)
	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/overview")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var body overviewResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Visibility.Current == nil || *body.Visibility.Current != 55 {
		t.Fatalf("current = %v, want 55", body.Visibility.Current)
	}
	if body.Visibility.Delta == nil || *body.Visibility.Delta != 15 {
		t.Fatalf("delta = %v, want 15", body.Visibility.Delta)
	}
	if len(body.Visibility.Trend) != 2 || len(body.Visibility.Trend[0].ResultIDs) != 1 {
		t.Fatalf("trend = %+v", body.Visibility.Trend)
	}
	if len(body.TopKeywords) != overviewPanelLimit {
		t.Fatalf("top keywords = %d, want %d", len(body.TopKeywords), overviewPanelLimit)
	}
	if len(body.TopCitedDomains) != 1 || len(body.TopCitedDomains[0].ResultIDs) != 1 {
		t.Fatalf("cited domains = %+v", body.TopCitedDomains)
	}

	// Tracked Co + Disc 1..3 (top-3 by coverage); Disc 4 and Dismissed Co dropped.
	if len(body.TopCompetitors) != 4 {
		t.Fatalf("top competitors = %d, want 4: %+v", len(body.TopCompetitors), body.TopCompetitors)
	}
	for _, c := range body.TopCompetitors {
		if c.Status == "dismissed" || c.Name == "Disc 4" {
			t.Fatalf("unexpected competitor in panel: %+v", c)
		}
	}
	if body.TopCompetitors[0].Status != "tracked" || body.TopCompetitors[0].VsSelf != -25 {
		t.Fatalf("first competitor = %+v", body.TopCompetitors[0])
	}
	// The panel shows 3 discovered, but the backlog count is the true total (4).
	if body.DiscoveredTotal != 4 {
		t.Fatalf("discovered total = %d, want 4", body.DiscoveredTotal)
	}

	if len(body.PromptChanges) != 2 {
		t.Fatalf("prompt changes = %v", body.PromptChanges)
	}
	if c := body.PromptChanges[0]; c.Date != "2026-06-01" || c.Added != 2 {
		t.Fatalf("prompt change[0] = %+v, want 2026-06-01 added 2", c)
	}
	if c := body.PromptChanges[1]; c.Date != "2026-07-01" || c.Replaced != 1 {
		t.Fatalf("prompt change[1] = %+v, want 2026-07-01 replaced 1", c)
	}
	if body.LatestRun == nil || body.LatestRun.Status != "completed" || body.LatestRun.ScheduledFor != "2026-07-13" {
		t.Fatalf("latest run = %+v", body.LatestRun)
	}
}

// TestOverviewEndpointNotFoundForCrossTenant confirms ListRuns is the ownership
// gate: its ErrNotFound becomes a 404 before any metric is shaped.
func TestOverviewEndpointNotFoundForCrossTenant(t *testing.T) {
	runs := &fakeRunStore{err: store.ErrNotFound}
	srv, cookie := newAuthedOverviewServer(t, runs, &fakeOverviewMetrics{})
	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/overview")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestOverviewEndpointEmptyBeforeFirstRun confirms an owned business with no
// runs yet returns 200 with a null latest_run and empty aggregates (the
// first-run state), not a 404.
func TestOverviewEndpointEmptyBeforeFirstRun(t *testing.T) {
	srv, cookie := newAuthedOverviewServer(t, &fakeRunStore{runs: []store.Run{}}, &fakeOverviewMetrics{})
	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/overview")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body overviewResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.LatestRun != nil || body.Visibility.Current != nil || body.Visibility.Delta != nil {
		t.Fatalf("expected empty first-run overview, got %+v", body)
	}
}
