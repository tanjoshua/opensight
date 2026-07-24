package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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

type fakeCompetitorStore struct {
	created             store.CompetitorRecord
	updated             store.CompetitorRecord
	createParams        store.CreateManualCompetitorParams
	statusParams        store.SetCompetitorStatusParams
	aliasParams         store.SuggestedAliasParams
	aliasRecord         store.CompetitorRecord
	createErr           error
	statusErr           error
	aliasErr            error
	aliasAction         string
	updateAliasesParams store.UpdateCompetitorAliasesParams
}

func (f *fakeCompetitorStore) CreateManual(_ context.Context, params store.CreateManualCompetitorParams) (store.CompetitorRecord, error) {
	f.createParams = params
	return f.created, f.createErr
}

func (f *fakeCompetitorStore) SetStatus(_ context.Context, params store.SetCompetitorStatusParams) (store.CompetitorRecord, error) {
	f.statusParams = params
	return f.updated, f.statusErr
}

func (f *fakeCompetitorStore) ApproveSuggestedAlias(_ context.Context, params store.SuggestedAliasParams) (store.CompetitorRecord, error) {
	f.aliasParams = params
	f.aliasAction = "approve"
	return f.aliasRecord, f.aliasErr
}

func (f *fakeCompetitorStore) RejectSuggestedAlias(_ context.Context, params store.SuggestedAliasParams) (store.CompetitorRecord, error) {
	f.aliasParams = params
	f.aliasAction = "reject"
	return f.aliasRecord, f.aliasErr
}

func (f *fakeCompetitorStore) UpdateAliases(_ context.Context, params store.UpdateCompetitorAliasesParams) (store.CompetitorRecord, error) {
	f.updateAliasesParams = params
	return f.aliasRecord, f.aliasErr
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
	return &Server{auth: f, businesses: businesses, competitors: &fakeCompetitorStore{}, competitorMetrics: m, secureCookies: false, sessionTTL: time.Hour},
		&http.Cookie{Name: sessionCookieName, Value: raw}
}

func newAuthedCompetitorWriteServer(t *testing.T, competitors competitorStore) (*Server, *http.Cookie) {
	t.Helper()
	srv, cookie := newAuthedCompetitorsServer(t, &fakeBusinessStore{}, &fakeCompetitorsMetrics{})
	srv.competitors = competitors
	return srv, cookie
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
		ResultIDs:     []domain.ID{resultID},
		Competitors: []metrics.CompetitorStat{
			{
				CompetitorID: mustHashV7(t, "01950000-0000-7000-8000-000000000201"), Name: "Tracked Co", Status: "tracked",
				Aliases: []string{"Tracked"}, SuggestedAliases: []string{"Tracked Health"},
				Mentioned: 9, TotalMentions: 12, MentionPercent: 45, AvgOrder: 2.1, VsSelf: -10,
				ResultIDs: []domain.ID{resultID},
				PerPrompt: []metrics.PromptAppearance{{PromptID: promptID, Text: "best clinic near me", ResultIDs: []domain.ID{resultID}}},
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
				PerPrompt: []metrics.PromptAppearance{{PromptID: promptID, Text: "best clinic near me", ResultIDs: []domain.ID{resultID}}},
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
	if len(body.Self.ResultIDs) != 1 {
		t.Fatalf("self result_ids = %+v, want one id", body.Self.ResultIDs)
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
	if len(first.Aliases) != 1 || len(first.SuggestedAliases) != 1 {
		t.Fatalf("first competitor aliases = %+v", first)
	}
	if len(first.ResultIDs) != 1 || len(first.PerPrompt) != 1 || len(first.PerPrompt[0].ResultIDs) != 1 || first.PerPrompt[0].PromptText == "" {
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

func TestCompetitorsEndpointLargeOffsetReturnsEmpty(t *testing.T) {
	m := &fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}
	srv, cookie := newAuthedCompetitorsServer(t, &fakeBusinessStore{}, m)
	maxInt := int(^uint(0) >> 1)

	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/competitors?offset="+strconv.Itoa(maxInt))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body competitorsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Competitors) != 0 || body.Paging.PageCount != 0 || body.Paging.Offset != maxInt {
		t.Fatalf("large offset page = competitors %d paging %+v, want empty at offset %d", len(body.Competitors), body.Paging, maxInt)
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

func TestAddManualCompetitor(t *testing.T) {
	competitorID := mustHashV7(t, "01950000-0000-7000-8000-000000000204")
	website := "https://rival.example"
	f := &fakeCompetitorStore{created: store.CompetitorRecord{
		ID: competitorID, BusinessID: mustHashV7(t, businessIDForTest),
		Name: "Rival Clinic", Website: &website, Aliases: []string{"Rival"},
		Source: "manual", Status: store.CompetitorStatusTracked,
	}}
	srv, cookie := newAuthedCompetitorWriteServer(t, f)

	rec := doAuthedPOST(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/competitors",
		`{"name":" Rival Clinic ","aliases":["Rival"],"website":"https://rival.example"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if f.createParams.TenantID.String() != tenantID || f.createParams.BusinessID.String() != businessIDForTest {
		t.Fatalf("tenant/business scope = %s/%s", f.createParams.TenantID, f.createParams.BusinessID)
	}
	if f.createParams.Name != " Rival Clinic " || f.createParams.Website == nil {
		t.Fatalf("create params = %+v", f.createParams)
	}
	var body competitorWriteResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID != competitorID.String() || body.Source != "manual" || body.Status != "tracked" {
		t.Fatalf("body = %+v", body)
	}
}

func TestAddManualCompetitorValidatesAndHidesCrossTenant(t *testing.T) {
	f := &fakeCompetitorStore{createErr: store.ErrNotFound}
	srv, cookie := newAuthedCompetitorWriteServer(t, f)

	blank := doAuthedPOST(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/competitors", `{"name":" "}`)
	if blank.Code != http.StatusBadRequest {
		t.Fatalf("blank status = %d, want 400", blank.Code)
	}
	crossTenant := doAuthedPOST(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/competitors", `{"name":"Rival"}`)
	if crossTenant.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status = %d, want 404", crossTenant.Code)
	}
}

func TestTrackAndDismissCompetitor(t *testing.T) {
	competitorID := mustHashV7(t, "01950000-0000-7000-8000-000000000205")
	f := &fakeCompetitorStore{updated: store.CompetitorRecord{
		ID: competitorID, Name: "Rival", Aliases: []string{},
		Source: "discovered", Status: store.CompetitorStatusTracked,
	}}
	srv, cookie := newAuthedCompetitorWriteServer(t, f)

	tracked := doAuthedPOST(t, srv, cookie, "/api/v1/competitors/"+competitorID.String()+"/track", "")
	if tracked.Code != http.StatusOK || f.statusParams.Status != store.CompetitorStatusTracked {
		t.Fatalf("track status = %d params=%+v body=%s", tracked.Code, f.statusParams, tracked.Body.String())
	}
	f.updated.Status = store.CompetitorStatusDismissed
	dismissed := doAuthedPOST(t, srv, cookie, "/api/v1/competitors/"+competitorID.String()+"/dismiss", "")
	if dismissed.Code != http.StatusOK || f.statusParams.Status != store.CompetitorStatusDismissed {
		t.Fatalf("dismiss status = %d params=%+v body=%s", dismissed.Code, f.statusParams, dismissed.Body.String())
	}
	if f.statusParams.TenantID.String() != tenantID {
		t.Fatalf("status tenant = %s", f.statusParams.TenantID)
	}
}

func TestSetCompetitorStatusNotFoundForCrossTenant(t *testing.T) {
	f := &fakeCompetitorStore{statusErr: store.ErrNotFound}
	srv, cookie := newAuthedCompetitorWriteServer(t, f)
	rec := doAuthedPOST(t, srv, cookie, "/api/v1/competitors/01950000-0000-7000-8000-000000000206/track", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestApproveAndRejectSuggestedAlias(t *testing.T) {
	competitorID := mustHashV7(t, "01950000-0000-7000-8000-000000000207")
	f := &fakeCompetitorStore{aliasRecord: store.CompetitorRecord{
		ID: competitorID, Name: "Rival", Aliases: []string{"Rival Medical"},
		SuggestedAliases: []string{}, Source: "discovered", Status: store.CompetitorStatusTracked,
	}}
	srv, cookie := newAuthedCompetitorWriteServer(t, f)

	approved := doAuthedPOST(t, srv, cookie,
		"/api/v1/competitors/"+competitorID.String()+"/suggested-aliases/approve",
		`{"alias":" Rival Medical "}`)
	if approved.Code != http.StatusOK || f.aliasAction != "approve" || f.aliasParams.Alias != "Rival Medical" {
		t.Fatalf("approve status=%d action=%q params=%+v body=%s", approved.Code, f.aliasAction, f.aliasParams, approved.Body.String())
	}
	var body competitorWriteResponse
	if err := json.NewDecoder(approved.Body).Decode(&body); err != nil {
		t.Fatalf("decode approve: %v", err)
	}
	if len(body.Aliases) != 1 || len(body.SuggestedAliases) != 0 {
		t.Fatalf("approve body = %+v", body)
	}

	f.aliasRecord.Aliases = []string{}
	rejected := doAuthedPOST(t, srv, cookie,
		"/api/v1/competitors/"+competitorID.String()+"/suggested-aliases/reject",
		`{"alias":"Rival Health"}`)
	if rejected.Code != http.StatusOK || f.aliasAction != "reject" || f.aliasParams.Alias != "Rival Health" {
		t.Fatalf("reject status=%d action=%q params=%+v body=%s", rejected.Code, f.aliasAction, f.aliasParams, rejected.Body.String())
	}
}

func TestSuggestedAliasValidationAndNotFound(t *testing.T) {
	f := &fakeCompetitorStore{aliasErr: store.ErrNotFound}
	srv, cookie := newAuthedCompetitorWriteServer(t, f)
	path := "/api/v1/competitors/01950000-0000-7000-8000-000000000208/suggested-aliases/approve"

	blank := doAuthedPOST(t, srv, cookie, path, `{"alias":" "}`)
	if blank.Code != http.StatusBadRequest {
		t.Fatalf("blank status = %d, want 400", blank.Code)
	}
	stale := doAuthedPOST(t, srv, cookie, path, `{"alias":"Stale Alias"}`)
	if stale.Code != http.StatusNotFound {
		t.Fatalf("stale status = %d, want 404", stale.Code)
	}
}

func TestPatchCompetitorAliases(t *testing.T) {
	competitorID := mustHashV7(t, "01950000-0000-7000-8000-000000000209")
	f := &fakeCompetitorStore{aliasRecord: store.CompetitorRecord{
		ID: competitorID, Name: "Rival", Aliases: []string{"Rival", "Rival Health"},
		SuggestedAliases: []string{"Pending"}, Status: store.CompetitorStatusTracked,
	}}
	srv, cookie := newAuthedCompetitorWriteServer(t, f)
	rec := doJSON(t, srv, http.MethodPatch, "/api/v1/competitors/"+competitorID.String(),
		`{"aliases":[" Rival ","rival","Rival Health","Rival, Incorporated"]}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(f.updateAliasesParams.Aliases) != 4 ||
		f.updateAliasesParams.Aliases[3] != "Rival, Incorporated" ||
		f.updateAliasesParams.TenantID.String() != tenantID {
		t.Fatalf("params = %+v", f.updateAliasesParams)
	}

	bad := doJSON(t, srv, http.MethodPatch, "/api/v1/competitors/"+competitorID.String(),
		`{"aliases":[""]}`, cookie)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("blank alias status = %d, want 400", bad.Code)
	}
}
