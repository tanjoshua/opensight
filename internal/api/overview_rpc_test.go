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

// newOverviewRPCServer builds a Server wired for direct OverviewService
// method calls (no HTTP/session interceptor involved — the session user is
// injected into ctx via businessRPCContext instead).
func newOverviewRPCServer(runs runStore, m overviewMetrics) *Server {
	return &Server{runs: runs, metrics: m}
}

// TestRPCGetOverviewShapesPayload ports TestOverviewEndpointShapesPayload:
// current/delta derived from the trend, top-N truncation, the tracked +
// top-3-discovered competitor filter (dismissed dropped), latest run, and
// result_ids carried through.
func TestRPCGetOverviewShapesPayload(t *testing.T) {
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
				{CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000101"), Name: "Tracked Co", Status: "tracked", MentionPercent: 30, VsSelf: -25, ResultIDs: []domain.ID{resultID},
					Trend: []metrics.CompetitorTrendPoint{
						{RunID: runID, ScheduledFor: time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC), Analyzed: 20, Mentioned: 5, Percent: 25, ResultIDs: []domain.ID{resultID}},
						{RunID: runID, ScheduledFor: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC), Analyzed: 20, Mentioned: 6, Percent: 30, ResultIDs: []domain.ID{resultID}},
					}},
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

	srv := newOverviewRPCServer(runs, m)
	resp, err := srv.GetOverview(businessRPCContext(t), connect.NewRequest(&opensightv1.GetOverviewRequest{BusinessId: businessIDForTest}))
	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}
	body := resp.Msg

	vis := body.GetVisibility()
	if vis.GetCurrent() != 55 {
		t.Fatalf("current = %v, want 55", vis.GetCurrent())
	}
	if vis.GetDelta() != 15 {
		t.Fatalf("delta = %v, want 15", vis.GetDelta())
	}
	if len(vis.GetTrend()) != 2 || len(vis.GetTrend()[0].GetResultIds()) != 1 {
		t.Fatalf("trend = %+v", vis.GetTrend())
	}
	if len(body.GetTopKeywords()) != overviewPanelLimit {
		t.Fatalf("top keywords = %d, want %d", len(body.GetTopKeywords()), overviewPanelLimit)
	}
	if len(body.GetTopCitedDomains()) != 1 || len(body.GetTopCitedDomains()[0].GetResultIds()) != 1 {
		t.Fatalf("cited domains = %+v", body.GetTopCitedDomains())
	}

	// Tracked Co + Disc 1..3 (top-3 by coverage); Disc 4 and Dismissed Co dropped.
	topCompetitors := body.GetTopCompetitors()
	if len(topCompetitors) != 4 {
		t.Fatalf("top competitors = %d, want 4: %+v", len(topCompetitors), topCompetitors)
	}
	for _, c := range topCompetitors {
		if c.GetStatus() == opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISMISSED || c.GetName() == "Disc 4" {
			t.Fatalf("unexpected competitor in panel: %+v", c)
		}
	}
	if topCompetitors[0].GetStatus() != opensightv1.CompetitorStatus_COMPETITOR_STATUS_TRACKED || topCompetitors[0].GetVsSelf() != -25 {
		t.Fatalf("first competitor = %+v", topCompetitors[0])
	}
	if tr := topCompetitors[0].GetTrend(); len(tr) != 2 || tr[1].GetPercent() != 30 || tr[1].GetRunId() != runID.String() {
		t.Fatalf("first competitor trend = %+v, want two points ending at 30%%", topCompetitors[0].GetTrend())
	}
	if body.GetDiscoveredTotal() != 4 {
		t.Fatalf("discovered total = %d, want 4", body.GetDiscoveredTotal())
	}

	changes := body.GetPromptChanges()
	if len(changes) != 2 {
		t.Fatalf("prompt changes = %+v", changes)
	}
	if changes[0].GetDate() != "2026-06-01" || changes[0].GetAdded() != 2 {
		t.Fatalf("prompt change[0] = %+v, want 2026-06-01 added 2", changes[0])
	}
	if changes[1].GetDate() != "2026-07-01" || changes[1].GetReplaced() != 1 {
		t.Fatalf("prompt change[1] = %+v, want 2026-07-01 replaced 1", changes[1])
	}

	latest := body.GetLatestRun()
	if latest == nil || latest.GetStatus() != opensightv1.RunStatus_RUN_STATUS_COMPLETED || latest.GetScheduledFor() != "2026-07-13" {
		t.Fatalf("latest run = %+v", latest)
	}
}

// TestRPCGetOverviewNotFoundForCrossTenant confirms ListRuns is the ownership
// gate: its ErrNotFound becomes NotFound before any metric is shaped.
func TestRPCGetOverviewNotFoundForCrossTenant(t *testing.T) {
	runs := &fakeRunStore{err: store.ErrNotFound}
	srv := newOverviewRPCServer(runs, &fakeOverviewMetrics{})
	_, err := srv.GetOverview(businessRPCContext(t), connect.NewRequest(&opensightv1.GetOverviewRequest{BusinessId: businessIDForTest}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

// TestRPCGetOverviewEmptyBeforeFirstRun confirms an owned business with no
// runs yet returns a null latest_run and empty aggregates (the first-run
// state), not an error.
func TestRPCGetOverviewEmptyBeforeFirstRun(t *testing.T) {
	srv := newOverviewRPCServer(&fakeRunStore{runs: []store.Run{}}, &fakeOverviewMetrics{})
	resp, err := srv.GetOverview(businessRPCContext(t), connect.NewRequest(&opensightv1.GetOverviewRequest{BusinessId: businessIDForTest}))
	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}
	body := resp.Msg
	if body.GetLatestRun() != nil || body.GetVisibility().Current != nil || body.GetVisibility().Delta != nil {
		t.Fatalf("expected empty first-run overview, got %+v", body)
	}
}
