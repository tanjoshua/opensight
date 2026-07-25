package api

import (
	"context"
	"math"
	"testing"

	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/metrics"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
)

// newCompetitorRPCServer builds a Server wired for direct CompetitorService
// method calls (no HTTP/session interceptor involved — the session user is
// injected into ctx via businessRPCContext instead).
func newCompetitorRPCServer(businesses businessStore, competitors competitorStore, m competitorsMetrics) *Server {
	return &Server{businesses: businesses, competitors: competitors, competitorMetrics: m}
}

// countingCompetitorsMetrics wraps fakeCompetitorsMetrics with a call
// counter, for proving GetBusiness runs before CompetitorStats — the
// tenant-leak gate-ordering proof (CompetitorStats itself returns an
// empty-but-successful result for an unowned business, so GetBusiness must
// be the sole gate).
type countingCompetitorsMetrics struct {
	fakeCompetitorsMetrics
	called int
}

func (m *countingCompetitorsMetrics) CompetitorStats(ctx context.Context, tenantID, businessID domain.ID) (metrics.CompetitorStats, error) {
	m.called++
	return m.fakeCompetitorsMetrics.CompetitorStats(ctx, tenantID, businessID)
}

// TestRPCListCompetitorsShapesPayload ports TestCompetitorsEndpointShapesPayload.
func TestRPCListCompetitorsShapesPayload(t *testing.T) {
	m := &fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, &fakeCompetitorStore{}, m)

	resp, err := srv.ListCompetitors(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCompetitorsRequest{BusinessId: businessIDForTest}))
	if err != nil {
		t.Fatalf("ListCompetitors: %v", err)
	}
	body := resp.Msg

	if body.GetSelf().GetTotalAnalyzed() != 20 || body.GetSelf().GetMentioned() != 11 || body.GetSelf().GetPercent() != 55 {
		t.Fatalf("self = %+v", body.GetSelf())
	}
	if len(body.GetSelf().GetResultIds()) != 1 {
		t.Fatalf("self result_ids = %+v, want one id", body.GetSelf().GetResultIds())
	}
	competitors := body.GetCompetitors()
	if len(competitors) != 3 {
		t.Fatalf("competitors = %d, want 3", len(competitors))
	}
	if competitors[0].GetName() != "Tracked Co" || competitors[2].GetName() != "Dismissed Co" {
		t.Fatalf("order = %v, %v", competitors[0].GetName(), competitors[2].GetName())
	}
	first := competitors[0]
	if first.GetMentionPercent() != 45 || first.GetVsSelf() != -10 || first.GetTotalMentions() != 12 || first.GetAvgOrder() != 2.1 {
		t.Fatalf("first competitor stats = %+v", first)
	}
	if len(first.GetAliases()) != 1 || len(first.GetSuggestedAliases()) != 1 {
		t.Fatalf("first competitor aliases = %+v", first)
	}
	if len(first.GetResultIds()) != 1 || len(first.GetPerPrompt()) != 1 || len(first.GetPerPrompt()[0].GetResultIds()) != 1 || first.GetPerPrompt()[0].GetPromptText() == "" {
		t.Fatalf("first per-prompt/result_ids = %+v", first)
	}
	if len(first.GetTrend()) != 1 || first.GetTrend()[0].GetPercent() != 45 || len(first.GetTrend()[0].GetResultIds()) != 1 {
		t.Fatalf("first trend = %+v", first.GetTrend())
	}
	if body.GetPaging().GetLimit() != int32(defaultCompetitorLimit) || body.GetPaging().GetOffset() != 0 || body.GetPaging().GetPageCount() != 3 {
		t.Fatalf("paging = %+v", body.GetPaging())
	}
}

// TestRPCListCompetitorsStatusFilter ports TestCompetitorsEndpointStatusFilter:
// UNSPECIFIED means all statuses, a real status filters and keeps history,
// and an out-of-range enum number is rejected.
func TestRPCListCompetitorsStatusFilter(t *testing.T) {
	m := &fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, &fakeCompetitorStore{}, m)

	all, err := srv.ListCompetitors(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCompetitorsRequest{
		BusinessId: businessIDForTest, Status: opensightv1.CompetitorStatus_COMPETITOR_STATUS_UNSPECIFIED,
	}))
	if err != nil {
		t.Fatalf("unspecified status: %v", err)
	}
	if len(all.Msg.GetCompetitors()) != 3 {
		t.Fatalf("unspecified status competitors = %d, want 3 (no filter)", len(all.Msg.GetCompetitors()))
	}

	dismissed, err := srv.ListCompetitors(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCompetitorsRequest{
		BusinessId: businessIDForTest, Status: opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISMISSED,
	}))
	if err != nil {
		t.Fatalf("dismissed status: %v", err)
	}
	got := dismissed.Msg.GetCompetitors()
	if len(got) != 1 || got[0].GetStatus() != opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISMISSED {
		t.Fatalf("dismissed filter = %+v", got)
	}
	if len(got[0].GetTrend()) != 1 || len(got[0].GetPerPrompt()) != 1 {
		t.Fatalf("dismissed lost history = %+v", got[0])
	}

	_, err = srv.ListCompetitors(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCompetitorsRequest{
		BusinessId: businessIDForTest, Status: opensightv1.CompetitorStatus(99),
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("out-of-range status code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

// TestRPCListCompetitorsPaginates ports TestCompetitorsEndpointPaginates.
func TestRPCListCompetitorsPaginates(t *testing.T) {
	m := &fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, &fakeCompetitorStore{}, m)

	resp, err := srv.ListCompetitors(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCompetitorsRequest{
		BusinessId: businessIDForTest, Limit: 1, Offset: 1,
	}))
	if err != nil {
		t.Fatalf("ListCompetitors: %v", err)
	}
	got := resp.Msg.GetCompetitors()
	if len(got) != 1 || got[0].GetName() != "Disc Co" {
		t.Fatalf("second page = %+v", got)
	}
	if resp.Msg.GetPaging().GetLimit() != 1 || resp.Msg.GetPaging().GetOffset() != 1 || resp.Msg.GetPaging().GetPageCount() != 1 {
		t.Fatalf("paging = %+v", resp.Msg.GetPaging())
	}
}

// TestRPCListCompetitorsLargeOffsetReturnsEmpty ports
// TestCompetitorsEndpointLargeOffsetReturnsEmpty, using math.MaxInt32 since
// the wire field is int32 (not a 64-bit max-int construct).
func TestRPCListCompetitorsLargeOffsetReturnsEmpty(t *testing.T) {
	m := &fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, &fakeCompetitorStore{}, m)

	resp, err := srv.ListCompetitors(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCompetitorsRequest{
		BusinessId: businessIDForTest, Offset: math.MaxInt32,
	}))
	if err != nil {
		t.Fatalf("ListCompetitors: %v", err)
	}
	if len(resp.Msg.GetCompetitors()) != 0 || resp.Msg.GetPaging().GetPageCount() != 0 || resp.Msg.GetPaging().GetOffset() != math.MaxInt32 {
		t.Fatalf("large offset page = %+v", resp.Msg)
	}
}

// TestRPCListCompetitorsNotFoundForCrossTenant confirms GetBusiness is the
// ownership gate strictly before CompetitorStats runs: NotFound, and the
// metrics call counter stays at 0.
func TestRPCListCompetitorsNotFoundForCrossTenant(t *testing.T) {
	m := &countingCompetitorsMetrics{fakeCompetitorsMetrics: fakeCompetitorsMetrics{stats: seedCompetitorStats(t)}}
	srv := newCompetitorRPCServer(&fakeBusinessStore{getErr: store.ErrNotFound}, &fakeCompetitorStore{}, m)

	_, err := srv.ListCompetitors(businessRPCContext(t), connect.NewRequest(&opensightv1.ListCompetitorsRequest{BusinessId: businessIDForTest}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
	if m.called != 0 {
		t.Fatalf("CompetitorStats called %d times before ownership proven, want 0", m.called)
	}
}

// TestRPCAddCompetitorCreates ports TestAddManualCompetitor.
func TestRPCAddCompetitorCreates(t *testing.T) {
	competitorID := mustHashV7(t, "01950000-0000-7000-8000-000000000204")
	website := "https://rival.example"
	f := &fakeCompetitorStore{created: store.CompetitorRecord{
		ID: competitorID, BusinessID: mustHashV7(t, businessIDForTest),
		Name: "Rival Clinic", Website: &website, Aliases: []string{"Rival"},
		Source: "manual", Status: store.CompetitorStatusTracked,
	}}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})

	resp, err := srv.AddCompetitor(businessRPCContext(t), connect.NewRequest(&opensightv1.AddCompetitorRequest{
		BusinessId: businessIDForTest, Name: " Rival Clinic ", Aliases: []string{"Rival"}, Website: "https://rival.example",
	}))
	if err != nil {
		t.Fatalf("AddCompetitor: %v", err)
	}
	if f.createParams.TenantID.String() != tenantID || f.createParams.BusinessID.String() != businessIDForTest {
		t.Fatalf("tenant/business scope = %s/%s", f.createParams.TenantID, f.createParams.BusinessID)
	}
	if f.createParams.Name != " Rival Clinic " || f.createParams.Website == nil {
		t.Fatalf("create params = %+v", f.createParams)
	}
	got := resp.Msg.GetCompetitor()
	if got.GetId() != competitorID.String() || got.GetSource() != opensightv1.CompetitorSource_COMPETITOR_SOURCE_MANUAL || got.GetStatus() != opensightv1.CompetitorStatus_COMPETITOR_STATUS_TRACKED {
		t.Fatalf("competitor = %+v", got)
	}
}

// TestRPCAddCompetitorValidatesAndHidesCrossTenant ports
// TestAddManualCompetitorValidatesAndHidesCrossTenant.
func TestRPCAddCompetitorValidatesAndHidesCrossTenant(t *testing.T) {
	f := &fakeCompetitorStore{createErr: store.ErrNotFound}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})

	_, err := srv.AddCompetitor(businessRPCContext(t), connect.NewRequest(&opensightv1.AddCompetitorRequest{
		BusinessId: businessIDForTest, Name: " ",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("blank name code = %v, want InvalidArgument", connect.CodeOf(err))
	}

	_, err = srv.AddCompetitor(businessRPCContext(t), connect.NewRequest(&opensightv1.AddCompetitorRequest{
		BusinessId: businessIDForTest, Name: "Rival",
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-tenant code = %v, want NotFound", connect.CodeOf(err))
	}
}

// TestRPCTrackAndDismissCompetitor ports TestTrackAndDismissCompetitor.
func TestRPCTrackAndDismissCompetitor(t *testing.T) {
	competitorID := mustHashV7(t, "01950000-0000-7000-8000-000000000205")
	f := &fakeCompetitorStore{updated: store.CompetitorRecord{
		ID: competitorID, Name: "Rival", Aliases: []string{},
		Source: "discovered", Status: store.CompetitorStatusTracked,
	}}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})

	tracked, err := srv.SetCompetitorStatus(businessRPCContext(t), connect.NewRequest(&opensightv1.SetCompetitorStatusRequest{
		CompetitorId: competitorID.String(), Status: opensightv1.CompetitorStatus_COMPETITOR_STATUS_TRACKED,
	}))
	if err != nil || f.statusParams.Status != store.CompetitorStatusTracked {
		t.Fatalf("track err=%v params=%+v", err, f.statusParams)
	}
	if tracked.Msg.GetCompetitor().GetStatus() != opensightv1.CompetitorStatus_COMPETITOR_STATUS_TRACKED {
		t.Fatalf("track response status = %v", tracked.Msg.GetCompetitor().GetStatus())
	}

	f.updated.Status = store.CompetitorStatusDismissed
	dismissed, err := srv.SetCompetitorStatus(businessRPCContext(t), connect.NewRequest(&opensightv1.SetCompetitorStatusRequest{
		CompetitorId: competitorID.String(), Status: opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISMISSED,
	}))
	if err != nil || f.statusParams.Status != store.CompetitorStatusDismissed {
		t.Fatalf("dismiss err=%v params=%+v", err, f.statusParams)
	}
	if dismissed.Msg.GetCompetitor().GetStatus() != opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISMISSED {
		t.Fatalf("dismiss response status = %v", dismissed.Msg.GetCompetitor().GetStatus())
	}
	if f.statusParams.TenantID.String() != tenantID {
		t.Fatalf("status tenant = %s", f.statusParams.TenantID)
	}
}

// TestRPCSetCompetitorStatusRejectsNonSettableStatus has no REST analogue:
// REST's /track and /dismiss endpoints hard-code the status, so there was no
// way to submit an invalid one. The merged RPC's discriminator must catch
// UNSPECIFIED, DISCOVERED, and any unrecognized enum number before the store
// is ever called.
func TestRPCSetCompetitorStatusRejectsNonSettableStatus(t *testing.T) {
	competitorID := mustHashV7(t, "01950000-0000-7000-8000-000000000205")
	cases := []struct {
		name   string
		status opensightv1.CompetitorStatus
	}{
		{"unspecified", opensightv1.CompetitorStatus_COMPETITOR_STATUS_UNSPECIFIED},
		{"discovered", opensightv1.CompetitorStatus_COMPETITOR_STATUS_DISCOVERED},
		{"out of range", opensightv1.CompetitorStatus(99)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCompetitorStore{}
			srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})
			_, err := srv.SetCompetitorStatus(businessRPCContext(t), connect.NewRequest(&opensightv1.SetCompetitorStatusRequest{
				CompetitorId: competitorID.String(), Status: tc.status,
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
			}
			if f.statusParams != (store.SetCompetitorStatusParams{}) {
				t.Fatalf("store called despite invalid status: params = %+v", f.statusParams)
			}
		})
	}
}

func TestRPCSetCompetitorStatusNotFoundForCrossTenant(t *testing.T) {
	f := &fakeCompetitorStore{statusErr: store.ErrNotFound}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})
	_, err := srv.SetCompetitorStatus(businessRPCContext(t), connect.NewRequest(&opensightv1.SetCompetitorStatusRequest{
		CompetitorId: "01950000-0000-7000-8000-000000000206", Status: opensightv1.CompetitorStatus_COMPETITOR_STATUS_TRACKED,
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

// TestRPCReviewSuggestedAliasApprovesAndRejects ports
// TestApproveAndRejectSuggestedAlias, and asserts the trimmed alias — not
// the raw request value — reaches the store.
func TestRPCReviewSuggestedAliasApprovesAndRejects(t *testing.T) {
	competitorID := mustHashV7(t, "01950000-0000-7000-8000-000000000207")
	f := &fakeCompetitorStore{aliasRecord: store.CompetitorRecord{
		ID: competitorID, Name: "Rival", Aliases: []string{"Rival Medical"},
		SuggestedAliases: []string{}, Source: "discovered", Status: store.CompetitorStatusTracked,
	}}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})

	approved, err := srv.ReviewSuggestedAlias(businessRPCContext(t), connect.NewRequest(&opensightv1.ReviewSuggestedAliasRequest{
		CompetitorId: competitorID.String(), Alias: " Rival Medical ", Decision: opensightv1.AliasDecision_ALIAS_DECISION_APPROVE,
	}))
	if err != nil || f.aliasAction != "approve" || f.aliasParams.Alias != "Rival Medical" {
		t.Fatalf("approve err=%v action=%q params=%+v", err, f.aliasAction, f.aliasParams)
	}
	if got := approved.Msg.GetCompetitor(); len(got.GetAliases()) != 1 || len(got.GetSuggestedAliases()) != 0 {
		t.Fatalf("approve body = %+v", got)
	}

	f.aliasRecord.Aliases = []string{}
	rejected, err := srv.ReviewSuggestedAlias(businessRPCContext(t), connect.NewRequest(&opensightv1.ReviewSuggestedAliasRequest{
		CompetitorId: competitorID.String(), Alias: "Rival Health", Decision: opensightv1.AliasDecision_ALIAS_DECISION_REJECT,
	}))
	if err != nil || f.aliasAction != "reject" || f.aliasParams.Alias != "Rival Health" {
		t.Fatalf("reject err=%v action=%q params=%+v", err, f.aliasAction, f.aliasParams)
	}
	_ = rejected
}

// TestRPCReviewSuggestedAliasValidationAndNotFound ports
// TestSuggestedAliasValidationAndNotFound, and confirms the decision
// discriminator is checked before the alias check — a request with both an
// invalid decision and a blank alias must report the decision failure (same
// precedent as ReplacePrompt's confirmed-before-text ordering).
func TestRPCReviewSuggestedAliasValidationAndNotFound(t *testing.T) {
	competitorID := "01950000-0000-7000-8000-000000000208"

	t.Run("blank alias", func(t *testing.T) {
		f := &fakeCompetitorStore{}
		srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})
		_, err := srv.ReviewSuggestedAlias(businessRPCContext(t), connect.NewRequest(&opensightv1.ReviewSuggestedAliasRequest{
			CompetitorId: competitorID, Alias: " ", Decision: opensightv1.AliasDecision_ALIAS_DECISION_APPROVE,
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
		}
	})

	t.Run("invalid decision takes precedence over blank alias", func(t *testing.T) {
		f := &fakeCompetitorStore{}
		srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})
		_, err := srv.ReviewSuggestedAlias(businessRPCContext(t), connect.NewRequest(&opensightv1.ReviewSuggestedAliasRequest{
			CompetitorId: competitorID, Alias: " ", Decision: opensightv1.AliasDecision_ALIAS_DECISION_UNSPECIFIED,
		}))
		cerr := asConnectError(t, err)
		if cerr.Code() != connect.CodeInvalidArgument {
			t.Fatalf("code = %v, want InvalidArgument", cerr.Code())
		}
		if cerr.Message() != "decision must be approve or reject" {
			t.Fatalf("message = %q, want the decision failure (not the blank-alias one)", cerr.Message())
		}
	})

	t.Run("stale alias not found", func(t *testing.T) {
		f := &fakeCompetitorStore{aliasErr: store.ErrNotFound}
		srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})
		_, err := srv.ReviewSuggestedAlias(businessRPCContext(t), connect.NewRequest(&opensightv1.ReviewSuggestedAliasRequest{
			CompetitorId: competitorID, Alias: "Stale Alias", Decision: opensightv1.AliasDecision_ALIAS_DECISION_APPROVE,
		}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
		}
	})
}

// TestRPCUpdateCompetitorAliasesReplaces ports TestPatchCompetitorAliases,
// confirming a value containing a comma (e.g. "Rival, Incorporated") survives
// as a single alias, not a delimiter.
func TestRPCUpdateCompetitorAliasesReplaces(t *testing.T) {
	competitorID := mustHashV7(t, "01950000-0000-7000-8000-000000000209")
	f := &fakeCompetitorStore{aliasRecord: store.CompetitorRecord{
		ID: competitorID, Name: "Rival", Aliases: []string{"Rival", "Rival Health"},
		SuggestedAliases: []string{"Pending"}, Status: store.CompetitorStatusTracked,
	}}
	srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})

	_, err := srv.UpdateCompetitorAliases(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateCompetitorAliasesRequest{
		CompetitorId: competitorID.String(),
		Aliases:      &opensightv1.StringList{Values: []string{" Rival ", "rival", "Rival Health", "Rival, Incorporated"}},
	}))
	if err != nil {
		t.Fatalf("UpdateCompetitorAliases: %v", err)
	}
	if len(f.updateAliasesParams.Aliases) != 4 ||
		f.updateAliasesParams.Aliases[3] != "Rival, Incorporated" ||
		f.updateAliasesParams.TenantID.String() != tenantID {
		t.Fatalf("params = %+v", f.updateAliasesParams)
	}

	_, err = srv.UpdateCompetitorAliases(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateCompetitorAliasesRequest{
		CompetitorId: competitorID.String(),
		Aliases:      &opensightv1.StringList{Values: []string{""}},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("blank alias code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

// TestRPCUpdateCompetitorAliasesRequiresList has no REST analogue: REST's
// bare `*[]string` and this RPC's StringList wrapper both distinguish
// omitted from present-but-empty, but the wrapper makes it a distinct proto
// nil-pointer case worth pinning directly.
func TestRPCUpdateCompetitorAliasesRequiresList(t *testing.T) {
	competitorID := "01950000-0000-7000-8000-00000000020a"

	t.Run("nil aliases rejected, store untouched", func(t *testing.T) {
		f := &fakeCompetitorStore{}
		srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})
		_, err := srv.UpdateCompetitorAliases(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateCompetitorAliasesRequest{
			CompetitorId: competitorID,
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
		}
		if f.updateAliasesParams.TenantID != (domain.ID{}) {
			t.Fatalf("store called despite nil aliases: params = %+v", f.updateAliasesParams)
		}
	})

	t.Run("empty StringList clears aliases", func(t *testing.T) {
		f := &fakeCompetitorStore{aliasRecord: store.CompetitorRecord{ID: mustHashV7(t, "01950000-0000-7000-8000-00000000020a")}}
		srv := newCompetitorRPCServer(&fakeBusinessStore{}, f, &fakeCompetitorsMetrics{})
		_, err := srv.UpdateCompetitorAliases(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateCompetitorAliasesRequest{
			CompetitorId: competitorID,
			Aliases:      &opensightv1.StringList{},
		}))
		if err != nil {
			t.Fatalf("UpdateCompetitorAliases: %v", err)
		}
		if f.updateAliasesParams.Aliases == nil || len(f.updateAliasesParams.Aliases) != 0 {
			t.Fatalf("aliases = %#v, want non-nil empty slice (the clear-all proof)", f.updateAliasesParams.Aliases)
		}
	})
}
