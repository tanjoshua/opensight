package api

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"opensight/internal/billing"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/llm"
	"opensight/internal/store"
	"opensight/internal/workflows"

	connect "connectrpc.com/connect"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
)

// newBusinessRPCServer builds a Server wired for direct BusinessService method
// calls (no HTTP/session interceptor involved — the session user is injected
// into ctx via businessRPCContext instead).
func newBusinessRPCServer(businesses businessStore, subscriptions subscriptionStore, proposals proposalStore, apply applyStore, temporal temporalClient) *Server {
	return &Server{
		businesses:        businesses,
		subscriptions:     subscriptions,
		proposals:         proposals,
		apply:             apply,
		temporal:          temporal,
		temporalTaskQueue: "opensight-test",
	}
}

// businessRPCContext returns a context carrying the same session user shape
// newAuthedOnboardingServer's fake session uses, for direct method calls that
// bypass the session interceptor.
func businessRPCContext(t *testing.T) context.Context {
	t.Helper()
	return withSessionUser(context.Background(), store.SessionUser{
		UserID:     mustHashV7(t, userID),
		TenantID:   mustHashV7(t, tenantID),
		Email:      "user@example.com",
		TenantName: "Acme Clinic",
		ExpiresAt:  time.Now().Add(time.Hour),
	})
}

// applyProposalPayloadProto decodes validApplyPayload (rpc_fakes_test.go) into
// llm.ProposalPayload and converts it to proto, so Apply tests exercise the
// real conversion path instead of hand-building a proto literal.
func applyProposalPayloadProto(t *testing.T) *opensightv1.ProposalPayload {
	t.Helper()
	return decodeApplyPayloadProto(t, validApplyPayload)
}

func decodeApplyPayloadProto(t *testing.T, raw string) *opensightv1.ProposalPayload {
	t.Helper()
	var payload llm.ProposalPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return proposalPayloadToProto(payload)
}

func TestRPCGetBusinessProfileAndPlan(t *testing.T) {
	businesses := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusActive)}
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	srv := newBusinessRPCServer(businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})

	resp, err := srv.GetBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.GetBusinessRequest{BusinessId: testBusinessID}))
	if err != nil {
		t.Fatalf("GetBusiness: %v", err)
	}
	b := resp.Msg.GetBusiness()
	if b.GetName() != "Old Clinic" || b.GetLocation().GetCountry() != "SG" ||
		b.GetPlan().GetSlug() != "starter" || b.GetPlan().GetPromptLimit() != 20 {
		t.Fatalf("business = %+v", b)
	}
}

func TestRPCPatchBusinessMergesAndClearsWebsite(t *testing.T) {
	businesses := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusActive)}
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	srv := newBusinessRPCServer(businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})

	name := "New Clinic"
	website := ""
	_, err := srv.UpdateBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateBusinessRequest{
		BusinessId: testBusinessID,
		Name:       &name,
		Website:    &website,
		Aliases:    &opensightv1.StringList{Values: []string{"Clinic, Incorporated"}},
		Location:   &opensightv1.Location{Address: "1 Road", Area: "Central", City: "Singapore", Country: "sg"},
	}))
	if err != nil {
		t.Fatalf("UpdateBusiness: %v", err)
	}
	if len(businesses.updated) != 1 {
		t.Fatalf("updates = %d", len(businesses.updated))
	}
	got := businesses.updated[0]
	if got.Name == nil || *got.Name != "New Clinic" || !got.WebsiteSet || got.Website != nil ||
		got.Category != nil || got.Services != nil || got.Aliases == nil ||
		len(*got.Aliases) != 1 || (*got.Aliases)[0] != "Clinic, Incorporated" ||
		got.Location == nil {
		t.Fatalf("partial params = %+v", got)
	}
}

func TestRPCPatchBusinessLeavesOmittedFieldsOutOfStoreUpdate(t *testing.T) {
	businesses := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusActive)}
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	srv := newBusinessRPCServer(businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})

	name := "Concurrent Name"
	if _, err := srv.UpdateBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateBusinessRequest{
		BusinessId: testBusinessID, Name: &name,
	})); err != nil {
		t.Fatalf("first UpdateBusiness: %v", err)
	}

	resp, err := srv.UpdateBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateBusinessRequest{
		BusinessId: testBusinessID,
		Services:   &opensightv1.StringList{Values: []string{"checkups", "screening"}},
	}))
	if err != nil {
		t.Fatalf("second UpdateBusiness: %v", err)
	}
	if len(businesses.updated) != 2 || businesses.updated[1].Name != nil ||
		businesses.updated[1].Services == nil {
		t.Fatalf("updates = %+v", businesses.updated)
	}
	if resp.Msg.GetBusiness().GetName() != "Concurrent Name" {
		t.Fatalf("name = %q, omitted second-patch field was overwritten", resp.Msg.GetBusiness().GetName())
	}
}

func TestRPCPatchBusinessRejectsDraftAndInvalidMergedProfile(t *testing.T) {
	draftStore := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusDraft)}
	srv := newBusinessRPCServer(draftStore, &fakeSubscriptionStore{}, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})
	name := "New"
	_, err := srv.UpdateBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateBusinessRequest{
		BusinessId: testBusinessID, Name: &name,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("draft code = %v, want FailedPrecondition", connect.CodeOf(err))
	}

	activeStore := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusActive)}
	srv = newBusinessRPCServer(activeStore, &fakeSubscriptionStore{}, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})
	_, err = srv.UpdateBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateBusinessRequest{
		BusinessId: testBusinessID,
		Location:   &opensightv1.Location{Address: "", Area: "", City: "", Country: "Singapore"},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid code = %v, want InvalidArgument: %v", connect.CodeOf(err), err)
	}
	if len(activeStore.updated) != 0 {
		t.Fatal("invalid patch reached store")
	}
}

func TestRPCCreateBusinessStartsGeneration(t *testing.T) {
	businesses := &fakeBusinessStore{business: store.Business{ID: mustHashV7(t, testBusinessID)}}
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	temporal := &fakeTemporalClient{}
	srv := newBusinessRPCServer(businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, temporal)

	_, err := srv.CreateBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.CreateBusinessRequest{
		Name: "Acme Clinic", Website: "acme.example",
	}))
	if err != nil {
		t.Fatalf("CreateBusiness: %v", err)
	}
	if len(businesses.created) != 1 {
		t.Fatalf("CreateBusiness calls = %d, want 1", len(businesses.created))
	}
	created := businesses.created[0]
	if created.Status != store.BusinessStatusDraft {
		t.Fatalf("created status = %q, want draft", created.Status)
	}
	if created.Website == nil || *created.Website != "acme.example" {
		t.Fatalf("created website = %v, want acme.example", created.Website)
	}
	if len(temporal.started) != 1 {
		t.Fatalf("ExecuteWorkflow calls = %d, want 1", len(temporal.started))
	}
	if temporal.started[0].TaskQueue != "opensight-test" {
		t.Fatalf("task queue = %q", temporal.started[0].TaskQueue)
	}
}

func TestRPCCreateBusinessMissingName(t *testing.T) {
	srv := newBusinessRPCServer(&fakeBusinessStore{}, &fakeSubscriptionStore{}, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})
	_, err := srv.CreateBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.CreateBusinessRequest{
		Name: "  ", Website: "x.example",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestRPCCreateBusinessPlanLookupFails(t *testing.T) {
	plans := &fakeSubscriptionStore{err: store.ErrNotFound}
	businesses := &fakeBusinessStore{}
	temporal := &fakeTemporalClient{}
	srv := newBusinessRPCServer(businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, temporal)

	_, err := srv.CreateBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.CreateBusinessRequest{Name: "Acme"}))
	// The rpcInternal guard: a plan lookup failure is always Internal, even
	// though the underlying error is store.ErrNotFound (which rpcError would
	// otherwise map to NotFound).
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if len(businesses.created) != 0 || len(temporal.started) != 0 {
		t.Fatalf("no create/start expected on plan failure; got created=%d started=%d", len(businesses.created), len(temporal.started))
	}
}

func TestRPCGetProposalReady(t *testing.T) {
	payload := json.RawMessage(`{
		"low_confidence": true,
		"profile": {
			"name": "Acme Clinic",
			"aliases": [],
			"category": "clinic",
			"services": [],
			"location": {"address": "", "area": "", "city": "", "country": "SG"}
		},
		"prompts": [{"text": "best clinic near me", "kind": "location"}]
	}`)
	proposals := &fakeProposalStore{proposal: store.ProfileProposal{Payload: payload}}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, &fakeSubscriptionStore{}, proposals, &fakeApplyStore{}, &fakeTemporalClient{})

	resp, err := srv.GetProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.GetProposalRequest{BusinessId: testBusinessID}))
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	state := resp.Msg.GetState()
	if state.GetStatus() != opensightv1.ProposalStatus_PROPOSAL_STATUS_READY {
		t.Fatalf("status = %v, want READY", state.GetStatus())
	}
	prompts := state.GetPayload().GetPrompts()
	if len(prompts) != 1 || prompts[0].GetText() != "best clinic near me" {
		t.Fatalf("prompts = %+v, want decoded proposal", prompts)
	}
}

func TestRPCGetProposalGenerating(t *testing.T) {
	proposals := &fakeProposalStore{getErr: store.ErrNotFound}
	temporal := &fakeTemporalClient{
		describeStatus: enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
		queryStage:     workflows.GenerationStageDrafting,
	}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, &fakeSubscriptionStore{}, proposals, &fakeApplyStore{}, temporal)

	resp, err := srv.GetProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.GetProposalRequest{BusinessId: testBusinessID}))
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	state := resp.Msg.GetState()
	if state.GetStatus() != opensightv1.ProposalStatus_PROPOSAL_STATUS_GENERATING {
		t.Fatalf("status = %v, want GENERATING", state.GetStatus())
	}
	if state.GetStage() != opensightv1.GenerationStage_GENERATION_STAGE_DRAFTING {
		t.Fatalf("stage = %v, want DRAFTING", state.GetStage())
	}
}

// TestGetProposalGeneratingDegradesOnQueryError: a failing stage query must not
// break the poll — status stays generating with the stage unset (UNSPECIFIED).
func TestRPCGetProposalGeneratingDegradesOnQueryError(t *testing.T) {
	proposals := &fakeProposalStore{getErr: store.ErrNotFound}
	temporal := &fakeTemporalClient{
		describeStatus: enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
		queryErr:       serviceerror.NewUnavailable("worker gone"),
	}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, &fakeSubscriptionStore{}, proposals, &fakeApplyStore{}, temporal)

	resp, err := srv.GetProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.GetProposalRequest{BusinessId: testBusinessID}))
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	state := resp.Msg.GetState()
	if state.GetStatus() != opensightv1.ProposalStatus_PROPOSAL_STATUS_GENERATING ||
		state.GetStage() != opensightv1.GenerationStage_GENERATION_STAGE_UNSPECIFIED {
		t.Fatalf("got status=%v stage=%v, want GENERATING with UNSPECIFIED stage", state.GetStatus(), state.GetStage())
	}
}

func TestRPCGetProposalFailedWhenWorkflowNotFound(t *testing.T) {
	proposals := &fakeProposalStore{getErr: store.ErrNotFound}
	temporal := &fakeTemporalClient{describeErr: serviceerror.NewNotFound("no workflow")}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, &fakeSubscriptionStore{}, proposals, &fakeApplyStore{}, temporal)

	resp, err := srv.GetProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.GetProposalRequest{BusinessId: testBusinessID}))
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if resp.Msg.GetState().GetStatus() != opensightv1.ProposalStatus_PROPOSAL_STATUS_FAILED {
		t.Fatalf("status = %v, want FAILED", resp.Msg.GetState().GetStatus())
	}
}

func TestRPCGetProposalNotFoundBusiness(t *testing.T) {
	// No pending proposal and the business is not owned/does not exist → NotFound.
	proposals := &fakeProposalStore{getErr: store.ErrNotFound}
	businesses := &fakeBusinessStore{getErr: store.ErrNotFound}
	srv := newBusinessRPCServer(businesses, &fakeSubscriptionStore{}, proposals, &fakeApplyStore{}, &fakeTemporalClient{})

	_, err := srv.GetProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.GetProposalRequest{BusinessId: testBusinessID}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestRPCRegenProposalDiscardsAndStarts(t *testing.T) {
	businesses := &fakeBusinessStore{business: store.Business{Status: store.BusinessStatusDraft, Name: "Acme"}}
	proposals := &fakeProposalStore{}
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	temporal := &fakeTemporalClient{}
	srv := newBusinessRPCServer(businesses, plans, proposals, &fakeApplyStore{}, temporal)

	resp, err := srv.RegenerateProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.RegenerateProposalRequest{BusinessId: testBusinessID}))
	if err != nil {
		t.Fatalf("RegenerateProposal: %v", err)
	}
	if proposals.discards != 1 {
		t.Fatalf("DiscardPending calls = %d, want 1", proposals.discards)
	}
	if len(temporal.started) != 1 {
		t.Fatalf("ExecuteWorkflow calls = %d, want 1", len(temporal.started))
	}
	if resp.Msg.GetState().GetStatus() != opensightv1.ProposalStatus_PROPOSAL_STATUS_GENERATING {
		t.Fatalf("status = %v, want GENERATING", resp.Msg.GetState().GetStatus())
	}
}

func TestRPCRegenProposalRejectedWhenNotDraft(t *testing.T) {
	businesses := &fakeBusinessStore{business: store.Business{Status: store.BusinessStatusActive}}
	proposals := &fakeProposalStore{}
	temporal := &fakeTemporalClient{}
	srv := newBusinessRPCServer(businesses, &fakeSubscriptionStore{}, proposals, &fakeApplyStore{}, temporal)

	_, err := srv.RegenerateProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.RegenerateProposalRequest{BusinessId: testBusinessID}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
	// An active business must not have its (nonexistent) pending proposal touched
	// nor a workflow started.
	if proposals.discards != 0 || len(temporal.started) != 0 {
		t.Fatalf("no discard/start expected; got discards=%d started=%d", proposals.discards, len(temporal.started))
	}
}

func TestRPCRegenProposalAlreadyRunning(t *testing.T) {
	businesses := &fakeBusinessStore{business: store.Business{Status: store.BusinessStatusDraft, Name: "Acme"}}
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	temporal := &fakeTemporalClient{execErr: serviceerror.NewWorkflowExecutionAlreadyStarted("already", "", "")}
	srv := newBusinessRPCServer(businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, temporal)

	_, err := srv.RegenerateProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.RegenerateProposalRequest{BusinessId: testBusinessID}))
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("code = %v, want AlreadyExists: %v", connect.CodeOf(err), err)
	}
}

func TestRPCApplyBusinessHappyPath(t *testing.T) {
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	apply := &fakeApplyStore{result: applyResult(t)}
	temporal := &fakeTemporalClient{}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	_, err := srv.ApplyProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.ApplyProposalRequest{
		BusinessId: testBusinessID, Payload: applyProposalPayloadProto(t),
	}))
	if err != nil {
		t.Fatalf("ApplyProposal: %v", err)
	}
	if len(apply.calls) != 1 {
		t.Fatalf("Apply calls = %d, want 1", len(apply.calls))
	}
	call := apply.calls[0]
	if call.BusinessID.String() != testBusinessID {
		t.Fatalf("apply business id = %s, want %s", call.BusinessID, testBusinessID)
	}
	if call.TenantID.String() != tenantID {
		t.Fatalf("apply tenant id = %s, want %s (tenant scoping)", call.TenantID, tenantID)
	}
	if call.Category != "orthopaedic clinic" {
		t.Fatalf("apply category = %q", call.Category)
	}
	if len(call.PromptTexts) != billing.Starter.PromptLimit {
		t.Fatalf("apply prompt texts = %d, want %d", len(call.PromptTexts), billing.Starter.PromptLimit)
	}
	if temporal.schedule == nil || temporal.schedule.creates != 1 {
		t.Fatalf("schedule creates = %v, want 1", temporal.schedule)
	}
	if len(temporal.started) != 1 {
		t.Fatalf("ExecuteWorkflow calls = %d, want 1", len(temporal.started))
	}
	wantPrefix := "run-" + testBusinessID + "-chatgpt-"
	if got := temporal.started[0].ID; len(got) < len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("first-run workflow id = %q, want prefix %q", got, wantPrefix)
	}
}

func TestRPCApplyBusinessValidationFails(t *testing.T) {
	// billing.Starter.PromptLimit is 20 but the payload carries 4 prompts →
	// count mismatch.
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	apply := &fakeApplyStore{result: applyResult(t)}
	temporal := &fakeTemporalClient{}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	_, err := srv.ApplyProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.ApplyProposalRequest{
		BusinessId: testBusinessID, Payload: decodeApplyPayloadProto(t, mismatchedCountApplyPayload),
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument: %v", connect.CodeOf(err), err)
	}
	if len(apply.calls) != 0 {
		t.Fatalf("Apply calls = %d, want 0 on validation failure", len(apply.calls))
	}
	if len(temporal.started) != 0 {
		t.Fatalf("ExecuteWorkflow calls = %d, want 0", len(temporal.started))
	}
}

func TestRPCApplyBusinessNotDraft(t *testing.T) {
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	apply := &fakeApplyStore{err: store.ErrBusinessNotDraft}
	temporal := &fakeTemporalClient{}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	_, err := srv.ApplyProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.ApplyProposalRequest{
		BusinessId: testBusinessID, Payload: applyProposalPayloadProto(t),
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition: %v", connect.CodeOf(err), err)
	}
	if len(temporal.started) != 0 {
		t.Fatalf("no first run expected when apply rejects; got %d", len(temporal.started))
	}
}

func TestRPCApplyBusinessNotFound(t *testing.T) {
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	apply := &fakeApplyStore{err: store.ErrNotFound}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, &fakeTemporalClient{})

	_, err := srv.ApplyProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.ApplyProposalRequest{
		BusinessId: testBusinessID, Payload: applyProposalPayloadProto(t),
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound: %v", connect.CodeOf(err), err)
	}
}

func TestRPCApplyBusinessScheduleError(t *testing.T) {
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	apply := &fakeApplyStore{result: applyResult(t)}
	temporal := &fakeTemporalClient{schedule: &fakeScheduleClient{err: errAny}}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	_, err := srv.ApplyProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.ApplyProposalRequest{
		BusinessId: testBusinessID, Payload: applyProposalPayloadProto(t),
	}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal: %v", connect.CodeOf(err), err)
	}
	// The DB apply committed, but the first run must not be started once schedule
	// creation failed.
	if len(temporal.started) != 0 {
		t.Fatalf("no first run expected after schedule failure; got %d", len(temporal.started))
	}
}

func TestRPCApplyBusinessFirstRunAlreadyStarted(t *testing.T) {
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	apply := &fakeApplyStore{result: applyResult(t)}
	temporal := &fakeTemporalClient{execErr: serviceerror.NewWorkflowExecutionAlreadyStarted("already", "", "")}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	_, err := srv.ApplyProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.ApplyProposalRequest{
		BusinessId: testBusinessID, Payload: applyProposalPayloadProto(t),
	}))
	if err != nil {
		t.Fatalf("ApplyProposal: %v (already-started first run should be idempotent)", err)
	}
}

// TestProposalPayloadRoundTrip guards the ToProto/FromProto conversion pair
// against a fully-populated payload (no nil slices — ToProto deliberately
// does not normalize nil vs empty, see business_conv.go).
func TestProposalPayloadRoundTrip(t *testing.T) {
	original := llm.ProposalPayload{
		LowConfidence: true,
		Profile: llm.ProposedProfile{
			Name:     "Acme Clinic",
			Aliases:  []string{"Acme", "Acme Ortho"},
			Category: "orthopaedic clinic",
			Services: []string{"consultation", "physiotherapy"},
			Location: llm.ProposedLocation{
				Address: "1 Road", Area: "Novena", City: "Singapore", Country: "SG",
			},
		},
		Prompts: []llm.ProposedPrompt{
			{Text: "best orthopaedic clinic in Singapore"},
			{Text: "top rated orthopaedic specialist in Singapore"},
		},
		Sources: []llm.ProposalSource{
			{URL: "https://acme.example", Title: "Acme Clinic", Domain: "acme.example"},
		},
	}

	roundTripped := proposalPayloadFromProto(proposalPayloadToProto(original))
	if !reflect.DeepEqual(original, roundTripped) {
		t.Fatalf("round trip mismatch:\n got  %+v\n want %+v", roundTripped, original)
	}

	if got := proposalPayloadFromProto(nil); !reflect.DeepEqual(got, llm.ProposalPayload{}) {
		t.Fatalf("proposalPayloadFromProto(nil) = %+v, want zero value", got)
	}
}

// TestRPCApplyProposalNilPayload guards against a panic when the client omits
// the payload entirely: proposalPayloadFromProto(nil) must produce a
// zero-value payload that fails llm.ValidateProposal naturally.
func TestRPCApplyProposalNilPayload(t *testing.T) {
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	apply := &fakeApplyStore{}
	srv := newBusinessRPCServer(&fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, &fakeTemporalClient{})

	_, err := srv.ApplyProposal(businessRPCContext(t), connect.NewRequest(&opensightv1.ApplyProposalRequest{
		BusinessId: testBusinessID, Payload: nil,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument: %v", connect.CodeOf(err), err)
	}
	if len(apply.calls) != 0 {
		t.Fatalf("Apply calls = %d, want 0", len(apply.calls))
	}
}

// TestRPCUpdateBusinessClearsListsWithEmptyStringList guards the
// GetValues()-returns-nil trap: a present-but-zero-value StringList must
// clear the list to a non-nil empty slice/JSON array, not a nil/null.
func TestRPCUpdateBusinessClearsListsWithEmptyStringList(t *testing.T) {
	businesses := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusActive)}
	plans := &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}}
	srv := newBusinessRPCServer(businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})

	_, err := srv.UpdateBusiness(businessRPCContext(t), connect.NewRequest(&opensightv1.UpdateBusinessRequest{
		BusinessId: testBusinessID,
		Aliases:    &opensightv1.StringList{},
		Services:   &opensightv1.StringList{},
	}))
	if err != nil {
		t.Fatalf("UpdateBusiness: %v", err)
	}
	if len(businesses.updated) != 1 {
		t.Fatalf("updates = %d, want 1", len(businesses.updated))
	}
	got := businesses.updated[0]
	if got.Aliases == nil || *got.Aliases == nil || len(*got.Aliases) != 0 {
		t.Fatalf("aliases = %v, want non-nil empty slice", got.Aliases)
	}
	if got.Services == nil {
		t.Fatal("services = nil, want a JSON array")
	}
	if string(*got.Services) != "[]" {
		t.Fatalf("services = %s, want []", string(*got.Services))
	}
}
