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

type fakeCitationMetrics struct {
	sources []metrics.CitationSource
	err     error
}

func (f *fakeCitationMetrics) CitationSources(context.Context, domain.ID, domain.ID) ([]metrics.CitationSource, error) {
	return f.sources, f.err
}

func newAuthedCitationsServer(t *testing.T, businesses businessStore, m citationsMetrics) (*Server, *http.Cookie) {
	t.Helper()
	raw := "citations-test-session"
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
	return &Server{auth: f, businesses: businesses, citationMetrics: m, secureCookies: false, sessionTTL: time.Hour},
		&http.Cookie{Name: sessionCookieName, Value: raw}
}

func seedCitationSources(t *testing.T) []metrics.CitationSource {
	t.Helper()
	resultA := mustHashV7(t, resultIDForTest)
	resultB := mustHashV7(t, "01950000-0000-7000-8000-0000000002a2")
	promptID := mustHashV7(t, promptIDForTest)
	return []metrics.CitationSource{
		{
			Domain:    "healthline.com",
			Frequency: 2,
			ResultIDs: []domain.ID{resultA, resultB},
			Subjects: metrics.CitationSubjectBreakdown{
				Business:   metrics.CitationSubjectStat{Frequency: 1, ResultIDs: []domain.ID{resultA}},
				Competitor: metrics.CitationSubjectStat{Frequency: 1, ResultIDs: []domain.ID{resultB}},
			},
			Pages: []metrics.CitationPage{
				{
					URL:       "https://healthline.com/root-canal",
					Title:     ptrString("Root canal guide"),
					Frequency: 1,
					ResultIDs: []domain.ID{resultA},
					Subjects:  metrics.CitationSubjectBreakdown{Business: metrics.CitationSubjectStat{Frequency: 1, ResultIDs: []domain.ID{resultA}}},
				},
			},
			Prompts: []metrics.CitationPrompt{
				{PromptID: promptID, Text: "best root canal clinic", Frequency: 2, ResultIDs: []domain.ID{resultA, resultB}},
			},
		},
		{
			Domain:    "other.com",
			Frequency: 1,
			ResultIDs: []domain.ID{resultB},
			Subjects:  metrics.CitationSubjectBreakdown{Other: metrics.CitationSubjectStat{Frequency: 1, ResultIDs: []domain.ID{resultB}}},
		},
	}
}

func TestCitationsEndpointShapesPayload(t *testing.T) {
	srv, cookie := newAuthedCitationsServer(t, &fakeBusinessStore{}, &fakeCitationMetrics{sources: seedCitationSources(t)})

	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/citations")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var body citationSourcesResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Domains) != 2 {
		t.Fatalf("domains = %d, want 2", len(body.Domains))
	}
	first := body.Domains[0]
	if first.Domain != "healthline.com" || first.Frequency != 2 || len(first.ResultIDs) != 2 {
		t.Fatalf("first domain = %+v", first)
	}
	if first.Subjects.Business.Frequency != 1 || len(first.Subjects.Business.ResultIDs) != 1 {
		t.Fatalf("business subject bucket = %+v", first.Subjects.Business)
	}
	if len(first.Pages) != 1 || first.Pages[0].Title == nil || *first.Pages[0].Title != "Root canal guide" || first.Pages[0].Subjects.Business.Frequency != 1 {
		t.Fatalf("pages = %+v", first.Pages)
	}
	if len(first.Prompts) != 1 || first.Prompts[0].PromptText != "best root canal clinic" || first.Prompts[0].Frequency != 2 {
		t.Fatalf("prompts = %+v", first.Prompts)
	}
	if body.Paging.Limit != defaultCitationLimit || body.Paging.Offset != 0 || body.Paging.PageCount != 2 {
		t.Fatalf("paging = %+v", body.Paging)
	}
}

func TestCitationsEndpointFiltersAndPaginates(t *testing.T) {
	srv, cookie := newAuthedCitationsServer(t, &fakeBusinessStore{}, &fakeCitationMetrics{sources: seedCitationSources(t)})

	filtered := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/citations?domain=healthline.com")
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered status = %d, want 200; body=%s", filtered.Code, filtered.Body.String())
	}
	var filteredBody citationSourcesResponse
	if err := json.NewDecoder(filtered.Body).Decode(&filteredBody); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(filteredBody.Domains) != 1 || filteredBody.Domains[0].Domain != "healthline.com" {
		t.Fatalf("filtered domains = %+v", filteredBody.Domains)
	}

	paged := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/citations?limit=1&offset=1")
	if paged.Code != http.StatusOK {
		t.Fatalf("paged status = %d, want 200; body=%s", paged.Code, paged.Body.String())
	}
	var pagedBody citationSourcesResponse
	if err := json.NewDecoder(paged.Body).Decode(&pagedBody); err != nil {
		t.Fatalf("decode paged: %v", err)
	}
	if len(pagedBody.Domains) != 1 || pagedBody.Domains[0].Domain != "other.com" {
		t.Fatalf("paged domains = %+v", pagedBody.Domains)
	}
	if pagedBody.Paging.Limit != 1 || pagedBody.Paging.Offset != 1 || pagedBody.Paging.PageCount != 1 {
		t.Fatalf("paging = %+v", pagedBody.Paging)
	}
}

func TestCitationsEndpointLargeOffsetReturnsEmpty(t *testing.T) {
	srv, cookie := newAuthedCitationsServer(t, &fakeBusinessStore{}, &fakeCitationMetrics{sources: seedCitationSources(t)})
	maxInt := int(^uint(0) >> 1)

	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/citations?offset="+strconv.Itoa(maxInt))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body citationSourcesResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Domains) != 0 || body.Paging.PageCount != 0 || body.Paging.Offset != maxInt {
		t.Fatalf("large offset page = domains %d paging %+v, want empty at offset %d", len(body.Domains), body.Paging, maxInt)
	}
}

func TestCitationsEndpointNotFoundForCrossTenant(t *testing.T) {
	srv, cookie := newAuthedCitationsServer(t, &fakeBusinessStore{getErr: store.ErrNotFound}, &fakeCitationMetrics{sources: seedCitationSources(t)})

	rec := doAuthedGET(t, srv, cookie, "/api/v1/businesses/"+businessIDForTest+"/citations")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
