package api

import (
	"math"
	"testing"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
)

// newCitationRPCServer builds a Server wired for direct CitationService
// method calls (no HTTP/session interceptor involved — the session user is
// injected into ctx via businessRPCContext instead).
func newCitationRPCServer(businesses businessStore, m citationsMetrics) *Server {
	return &Server{businesses: businesses, citationMetrics: m}
}

func TestRPCListCitationSourcesShapesPayload(t *testing.T) {
	srv := newCitationRPCServer(&fakeBusinessStore{}, &fakeCitationMetrics{sources: seedCitationSources(t)})

	resp, err := srv.ListCitationSources(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCitationSourcesRequest{BusinessId: businessIDForTest}))
	if err != nil {
		t.Fatalf("ListCitationSources: %v", err)
	}
	body := resp.Msg
	if len(body.GetDomains()) != 2 {
		t.Fatalf("domains = %d, want 2", len(body.GetDomains()))
	}
	first := body.GetDomains()[0]
	if first.GetDomain() != "healthline.com" || first.GetFrequency() != 2 || len(first.GetResultIds()) != 2 {
		t.Fatalf("first domain = %+v", first)
	}
	if first.GetSubjects().GetBusiness().GetFrequency() != 1 || len(first.GetSubjects().GetBusiness().GetResultIds()) != 1 {
		t.Fatalf("business subject bucket = %+v", first.GetSubjects().GetBusiness())
	}
	pages := first.GetPages()
	if len(pages) != 1 || pages[0].GetTitle() != "Root canal guide" || pages[0].GetSubjects().GetBusiness().GetFrequency() != 1 {
		t.Fatalf("pages = %+v", pages)
	}
	prompts := first.GetPrompts()
	if len(prompts) != 1 || prompts[0].GetPromptText() != "best root canal clinic" || prompts[0].GetFrequency() != 2 {
		t.Fatalf("prompts = %+v", prompts)
	}
	if body.GetPaging().GetLimit() != int32(defaultCitationLimit) || body.GetPaging().GetOffset() != 0 || body.GetPaging().GetPageCount() != 2 {
		t.Fatalf("paging = %+v", body.GetPaging())
	}
}

func TestRPCListCitationSourcesFiltersAndPaginates(t *testing.T) {
	srv := newCitationRPCServer(&fakeBusinessStore{}, &fakeCitationMetrics{sources: seedCitationSources(t)})

	filtered, err := srv.ListCitationSources(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCitationSourcesRequest{
		BusinessId: businessIDForTest, Domain: "healthline.com",
	}))
	if err != nil {
		t.Fatalf("ListCitationSources (filtered): %v", err)
	}
	if domains := filtered.Msg.GetDomains(); len(domains) != 1 || domains[0].GetDomain() != "healthline.com" {
		t.Fatalf("filtered domains = %+v", domains)
	}

	paged, err := srv.ListCitationSources(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCitationSourcesRequest{
		BusinessId: businessIDForTest, Limit: 1, Offset: 1,
	}))
	if err != nil {
		t.Fatalf("ListCitationSources (paged): %v", err)
	}
	pagedBody := paged.Msg
	if domains := pagedBody.GetDomains(); len(domains) != 1 || domains[0].GetDomain() != "other.com" {
		t.Fatalf("paged domains = %+v", domains)
	}
	if pagedBody.GetPaging().GetLimit() != 1 || pagedBody.GetPaging().GetOffset() != 1 || pagedBody.GetPaging().GetPageCount() != 1 {
		t.Fatalf("paging = %+v", pagedBody.GetPaging())
	}
}

// TestRPCListCitationSourcesLargeOffsetReturnsEmpty guards the windowing
// clamp against a huge offset. math.MaxInt32 is used (not a 64-bit max int)
// since proto's int32 offset field can't represent a full 64-bit value.
func TestRPCListCitationSourcesLargeOffsetReturnsEmpty(t *testing.T) {
	srv := newCitationRPCServer(&fakeBusinessStore{}, &fakeCitationMetrics{sources: seedCitationSources(t)})

	resp, err := srv.ListCitationSources(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCitationSourcesRequest{
		BusinessId: businessIDForTest, Offset: math.MaxInt32,
	}))
	if err != nil {
		t.Fatalf("ListCitationSources: %v", err)
	}
	body := resp.Msg
	if len(body.GetDomains()) != 0 || body.GetPaging().GetPageCount() != 0 || body.GetPaging().GetOffset() != math.MaxInt32 {
		t.Fatalf("large offset page = domains %d paging %+v, want empty at offset %d", len(body.GetDomains()), body.GetPaging(), int32(math.MaxInt32))
	}
}

func TestRPCListCitationSourcesNotFoundForCrossTenant(t *testing.T) {
	srv := newCitationRPCServer(&fakeBusinessStore{getErr: store.ErrNotFound}, &fakeCitationMetrics{sources: seedCitationSources(t)})

	_, err := srv.ListCitationSources(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCitationSourcesRequest{BusinessId: businessIDForTest}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}
