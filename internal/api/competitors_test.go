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

type fakeCompetitorsMetrics struct {
	stats metrics.CompetitorStats
	err   error
}

func (f *fakeCompetitorsMetrics) CompetitorStats(context.Context, domain.ID, domain.ID) (metrics.CompetitorStats, error) {
	return f.stats, f.err
}

func newAuthedCompetitorsServer(t *testing.T, businesses businessStore, m competitorsMetrics) (*Server, *http.Cookie) {
	t.Helper()
	raw := "competitors-test-session"
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
	return &Server{auth: f, businesses: businesses, competitorMetrics: m, secureCookies: false, sessionTTL: time.Hour},
		&http.Cookie{Name: sessionCookieName, Value: raw}
}

// seedCompetitorStats builds a three-competitor comparison (coverage-desc) with
// one of each status; the dismissed one carries a full trend + per-prompt history.
func seedCompetitorStats(t *testing.T) metrics.CompetitorStats {
	t.Helper()
	resultID := mustHashV7(t, resultIDForTest)
	runID := mustHashV7(t, runIDForTest)
	promptID := mustHashV7(t, promptIDForTest)
	return metrics.CompetitorStats{
		TotalAnalyzed: 20,
		SelfMentioned: 11,
		SelfPercent:   55,
		Competitors: []metrics.CompetitorStat{
			{
				CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000201"), Name: "Tracked Co", Status: "tracked",
				Mentioned: 9, TotalMentions: 12, MentionPercent: 45, AvgOrder: 2.1, VsSelf: -10,
				ResultIDs: []domain.ID{resultID},
				PerPrompt: []metrics.PromptAppearance{{PromptID: promptID, ResultIDs: []domain.ID{resultID}}},
				Trend:     []metrics.CompetitorTrendPoint{{RunID: runID, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Analyzed: 20, Mentioned: 9, Percent: 45, ResultIDs: []domain.ID{resultID}}},
			},
			{
				CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000202"), Name: "Disc Co", Status: "discovered",
				Mentioned: 7, TotalMentions: 8, MentionPercent: 35, AvgOrder: 3.4, VsSelf: -20,
				ResultIDs: []domain.ID{resultID},
				PerPrompt: []metrics.PromptAppearance{},
				Trend:     []metrics.CompetitorTrendPoint{},
			},
			{
				CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000203"), Name: "Dismissed Co", Status: "dismissed",
				Mentioned: 5, TotalMentions: 6, MentionPercent: 25, AvgOrder: 4.0, VsSelf: -30,
				ResultIDs: []domain.ID{resultID},
				PerPrompt: []metrics.PromptAppearance{{PromptID: promptID, ResultIDs: []domain.ID{resultID}}},
				Trend:     []metrics.CompetitorTrendPoint{{RunID: runID, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Analyzed: 20, Mentioned: 5, Percent: 25, ResultIDs: []domain.ID{resultID}}},
			},
		},
	}
}

// TestCompetitorsEndpointShapesPayload pins the full shape: self baseline, every
// competitor coverage-ranked with per-prompt + trend history, and result_ids
// carried through the top-level and nested aggregates.
func TestCompetitorsEndpointShapesPayload(t *testing.T) {
	m := &fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}
	srv, cookie := newAuthedCompetitorsServer(t, &fakeBusinessStore{}, m)

	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/competitors")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var body competitorsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Self.TotalAnalyzed != 20 || body.Self.Mentioned != 11 || body.Self.Percent != 55 {
		t.Fatalf("self = %+v", body.Self)
	}
	if len(body.Competitors) != 3 {
		t.Fatalf("competitors = %d, want 3", len(body.Competitors))
	}
	// Coverage-desc order preserved from the metrics layer.
	if body.Competitors[0].Name != "Tracked Co" || body.Competitors[2].Name != "Dismissed Co" {
		t.Fatalf("order = %v, %v", body.Competitors[0].Name, body.Competitors[2].Name)
	}
	first := body.Competitors[0]
	if first.MentionPercent != 45 || first.VsSelf != -10 || first.TotalMentions != 12 || first.AvgOrder != 2.1 {
		t.Fatalf("first competitor stats = %+v", first)
	}
	if len(first.ResultIDs) != 1 || len(first.PerPrompt) != 1 || len(first.PerPrompt[0].ResultIDs) != 1 {
		t.Fatalf("first per-prompt/result_ids = %+v", first)
	}
	if len(first.Trend) != 1 || first.Trend[0].Percent != 45 || len(first.Trend[0].ResultIDs) != 1 {
		t.Fatalf("first trend = %+v", first.Trend)
	}
	if body.Paging.Limit != defaultCompetitorLimit || body.Paging.Offset != 0 || body.Paging.PageCount != 3 {
		t.Fatalf("paging = %+v", body.Paging)
	}
}

// TestCompetitorsEndpointStatusFilter confirms ?status is a display filter that
// selects one status while preserving that competitor's full history (dismissed
// keeps its trend + per-prompt), and that an unknown status is rejected.
func TestCompetitorsEndpointStatusFilter(t *testing.T) {
	m := &fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}
	srv, cookie := newAuthedCompetitorsServer(t, &fakeBusinessStore{}, m)

	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/competitors?status=dismissed")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body competitorsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Competitors) != 1 || body.Competitors[0].Status != "dismissed" {
		t.Fatalf("dismissed filter = %+v", body.Competitors)
	}
	// History is retained through the display filter.
	if len(body.Competitors[0].Trend) != 1 || len(body.Competitors[0].PerPrompt) != 1 {
		t.Fatalf("dismissed lost history = %+v", body.Competitors[0])
	}

	bad := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/competitors?status=bogus")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bogus status = %d, want 400", bad.Code)
	}
}

// TestCompetitorsEndpointPaginates confirms limit/offset window the coverage-
// ranked list and report the page in the paging block.
func TestCompetitorsEndpointPaginates(t *testing.T) {
	m := &fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}
	srv, cookie := newAuthedCompetitorsServer(t, &fakeBusinessStore{}, m)

	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/competitors?limit=1&offset=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body competitorsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Competitors) != 1 || body.Competitors[0].Name != "Disc Co" {
		t.Fatalf("second page = %+v", body.Competitors)
	}
	if body.Paging.Limit != 1 || body.Paging.Offset != 1 || body.Paging.PageCount != 1 {
		t.Fatalf("paging = %+v", body.Paging)
	}
}

// TestCompetitorsEndpointNotFoundForCrossTenant confirms GetBusiness is the
// ownership gate: its ErrNotFound becomes a 404 before any metric is shaped.
func TestCompetitorsEndpointNotFoundForCrossTenant(t *testing.T) {
	m := &fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}
	srv, cookie := newAuthedCompetitorsServer(t, &fakeBusinessStore{getErr: store.ErrNotFound}, m)

	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/competitors")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
