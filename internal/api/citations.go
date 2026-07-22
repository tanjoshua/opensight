package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"opensight/internal/domain"
	"opensight/internal/metrics"
)

const (
	defaultCitationLimit = 50
	maxCitationLimit     = 100
)

// citationsMetrics is the metrics seam for MET-6. CitationSources is tenant-
// scoped and uses the same analyzed-result base as Overview.
type citationsMetrics interface {
	CitationSources(ctx context.Context, tenantID, businessID domain.ID) ([]metrics.CitationSource, error)
}

type citationSourcesResponse struct {
	Domains []citationSourceResponse `json:"domains"`
	Paging  pagingResponse           `json:"paging"`
}

type citationSourceResponse struct {
	Domain    string                   `json:"domain"`
	Frequency int                      `json:"frequency"`
	Subjects  citationSubjectsResponse `json:"subjects"`
	ResultIDs []string                 `json:"result_ids"`
	Pages     []citationPageResponse   `json:"pages"`
	Prompts   []citationPromptResponse `json:"prompts"`
}

type citationSubjectsResponse struct {
	Business   citationSubjectResponse `json:"business"`
	Competitor citationSubjectResponse `json:"competitor"`
	Other      citationSubjectResponse `json:"other"`
	Unknown    citationSubjectResponse `json:"unknown"`
}

type citationSubjectResponse struct {
	Frequency int      `json:"frequency"`
	ResultIDs []string `json:"result_ids"`
}

type citationPageResponse struct {
	URL       string                   `json:"url"`
	Title     *string                  `json:"title"`
	Frequency int                      `json:"frequency"`
	Subjects  citationSubjectsResponse `json:"subjects"`
	ResultIDs []string                 `json:"result_ids"`
}

type citationPromptResponse struct {
	PromptID   string   `json:"prompt_id"`
	PromptText string   `json:"prompt_text"`
	Frequency  int      `json:"frequency"`
	ResultIDs  []string `json:"result_ids"`
}

// handleListCitations serves GET /businesses/:id/citations (MET-6): domains,
// cited pages, associated prompts, and subject splits. GetBusiness is the
// ownership gate before the metrics query runs.
func (s *Server) handleListCitations(w http.ResponseWriter, r *http.Request) {
	if s.businesses == nil || s.citationMetrics == nil {
		s.writeInternalError(w, "list citations: store missing", errors.New("business store and metrics are required"))
		return
	}

	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "list citations: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}
	domainFilter := r.URL.Query().Get("domain")
	limit, ok := positiveIntParam(w, r.URL.Query().Get("limit"), defaultCitationLimit, maxCitationLimit, "limit")
	if !ok {
		return
	}
	offset, ok := nonNegativeIntParam(w, r.URL.Query().Get("offset"), 0, "offset")
	if !ok {
		return
	}

	ctx := r.Context()
	if _, err := s.businesses.GetBusiness(ctx, su.TenantID, businessID); err != nil {
		writeStoreError(w, err)
		return
	}

	sources, err := s.citationMetrics.CitationSources(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	filtered := sources
	if domainFilter != "" {
		filtered = slices.DeleteFunc(slices.Clone(sources), func(source metrics.CitationSource) bool {
			return source.Domain != domainFilter
		})
	}
	start := min(offset, len(filtered))
	end := start + min(limit, len(filtered)-start)
	page := filtered[start:end]

	resp := citationSourcesResponse{
		Domains: make([]citationSourceResponse, 0, len(page)),
		Paging:  pagingResponse{Limit: limit, Offset: offset, PageCount: len(page)},
	}
	for _, source := range page {
		resp.Domains = append(resp.Domains, citationSourceToResponse(source))
	}
	writeJSON(w, http.StatusOK, resp)
}

func citationSourceToResponse(source metrics.CitationSource) citationSourceResponse {
	resp := citationSourceResponse{
		Domain:    source.Domain,
		Frequency: source.Frequency,
		Subjects:  citationSubjectsToResponse(source.Subjects),
		ResultIDs: idStrings(source.ResultIDs),
		Pages:     make([]citationPageResponse, 0, len(source.Pages)),
		Prompts:   make([]citationPromptResponse, 0, len(source.Prompts)),
	}
	for _, page := range source.Pages {
		resp.Pages = append(resp.Pages, citationPageResponse{
			URL:       page.URL,
			Title:     page.Title,
			Frequency: page.Frequency,
			Subjects:  citationSubjectsToResponse(page.Subjects),
			ResultIDs: idStrings(page.ResultIDs),
		})
	}
	for _, prompt := range source.Prompts {
		resp.Prompts = append(resp.Prompts, citationPromptResponse{
			PromptID:   prompt.PromptID.String(),
			PromptText: prompt.Text,
			Frequency:  prompt.Frequency,
			ResultIDs:  idStrings(prompt.ResultIDs),
		})
	}
	return resp
}

func citationSubjectsToResponse(subjects metrics.CitationSubjectBreakdown) citationSubjectsResponse {
	return citationSubjectsResponse{
		Business:   citationSubjectToResponse(subjects.Business),
		Competitor: citationSubjectToResponse(subjects.Competitor),
		Other:      citationSubjectToResponse(subjects.Other),
		Unknown:    citationSubjectToResponse(subjects.Unknown),
	}
}

func citationSubjectToResponse(subject metrics.CitationSubjectStat) citationSubjectResponse {
	return citationSubjectResponse{
		Frequency: subject.Frequency,
		ResultIDs: idStrings(subject.ResultIDs),
	}
}
