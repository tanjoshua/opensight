package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/store"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

type fakePlanStore struct {
	plan store.Plan
	err  error
}

func (f *fakePlanStore) GetTenantPlan(context.Context, domain.ID) (store.Plan, error) {
	return f.plan, f.err
}

type fakeProposalStore struct {
	proposal   store.ProfileProposal
	getErr     error
	discardErr error
	discards   int
}

func (f *fakeProposalStore) GetPending(context.Context, domain.ID, domain.ID) (store.ProfileProposal, error) {
	return f.proposal, f.getErr
}

func (f *fakeProposalStore) DiscardPending(context.Context, domain.ID, domain.ID) error {
	f.discards++
	return f.discardErr
}

type fakeTemporalClient struct {
	execErr        error
	started        []client.StartWorkflowOptions
	describeStatus enumspb.WorkflowExecutionStatus
	describeErr    error
	schedule       *fakeScheduleClient
}

func (f *fakeTemporalClient) ExecuteWorkflow(_ context.Context, opts client.StartWorkflowOptions, _ interface{}, _ ...interface{}) (client.WorkflowRun, error) {
	f.started = append(f.started, opts)
	return nil, f.execErr
}

func (f *fakeTemporalClient) DescribeWorkflowExecution(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: f.describeStatus},
	}, nil
}

func (f *fakeTemporalClient) ScheduleClient() client.ScheduleClient {
	if f.schedule == nil {
		f.schedule = &fakeScheduleClient{}
	}
	return f.schedule
}

// fakeScheduleClient records Create calls and can inject an error. Only Create is
// exercised; the embedded nil client.ScheduleClient satisfies the rest of the
// interface (never called in these tests).
type fakeScheduleClient struct {
	client.ScheduleClient
	creates int
	err     error
}

func (f *fakeScheduleClient) Create(context.Context, client.ScheduleOptions) (client.ScheduleHandle, error) {
	f.creates++
	return nil, f.err
}

type fakeApplyStore struct {
	result store.ApplyProposalResult
	err    error
	calls  []store.ApplyProposalParams
}

func (f *fakeApplyStore) Apply(_ context.Context, params store.ApplyProposalParams) (store.ApplyProposalResult, error) {
	f.calls = append(f.calls, params)
	if f.err != nil {
		return store.ApplyProposalResult{}, f.err
	}
	return f.result, nil
}

func newAuthedOnboardingServer(t *testing.T, businesses businessStore, plans planStore, proposals proposalStore, apply applyStore, temporal temporalClient) (*Server, *http.Cookie) {
	t.Helper()
	raw := "onboarding-test-session"
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
	return &Server{
			auth:              f,
			businesses:        businesses,
			plans:             plans,
			proposals:         proposals,
			apply:             apply,
			temporal:          temporal,
			temporalTaskQueue: "opensight-test",
			secureCookies:     false,
			sessionTTL:        time.Hour,
		},
		&http.Cookie{Name: sessionCookieName, Value: raw}
}

const testBusinessID = "01950000-0000-7000-8000-0000000000c3"

func setupBusinessFixture(t *testing.T, status store.BusinessStatus) store.Business {
	t.Helper()
	category := "clinic"
	website := "https://old.example"
	return store.Business{
		ID: mustHashV7(t, testBusinessID), TenantID: mustHashV7(t, tenantID),
		Status: status, Name: "Old Clinic", Website: &website,
		Aliases: []string{"Old"}, Category: &category,
		Practitioners: json.RawMessage(`[{"name":"Dr Tan","role":"dentist"}]`),
		Services:      json.RawMessage(`["checkups"]`),
		Location:      json.RawMessage(`{"address":"1 Road","area":"Central","city":"Singapore","country":"SG"}`),
	}
}

func TestGetBusinessProfileAndPlan(t *testing.T) {
	businesses := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusActive)}
	plans := &fakePlanStore{plan: store.Plan{
		Slug: "starter", PromptLimit: 20, RunInterval: "weekly", Platforms: []string{"chatgpt"},
	}}
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/businesses/"+testBusinessID, "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body businessDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Name != "Old Clinic" || body.Location.Country != "SG" || body.Plan.Slug != "starter" ||
		body.Plan.PromptLimit != 20 || len(body.Practitioners) != 1 {
		t.Fatalf("body = %+v", body)
	}
}

func TestPatchBusinessMergesAndClearsWebsite(t *testing.T) {
	businesses := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusActive)}
	plans := &fakePlanStore{plan: store.Plan{Slug: "starter", PromptLimit: 20}}
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})
	rec := doJSON(t, srv, http.MethodPatch, "/api/v1/businesses/"+testBusinessID,
		`{"name":"New Clinic","website":"","aliases":["Clinic, Incorporated"],"location":{"address":"1 Road","area":"Central","city":"Singapore","country":"sg"}}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(businesses.updated) != 1 {
		t.Fatalf("updates = %d", len(businesses.updated))
	}
	got := businesses.updated[0]
	if got.Name == nil || *got.Name != "New Clinic" || !got.WebsiteSet || got.Website != nil ||
		got.Category != nil || got.Services != nil || got.Aliases == nil ||
		len(*got.Aliases) != 1 || (*got.Aliases)[0] != "Clinic, Incorporated" ||
		got.Practitioners != nil ||
		got.Location == nil {
		t.Fatalf("partial params = %+v", got)
	}
}

func TestPatchBusinessLeavesOmittedFieldsOutOfStoreUpdate(t *testing.T) {
	businesses := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusActive)}
	plans := &fakePlanStore{plan: store.Plan{Slug: "starter", PromptLimit: 20}}
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})

	first := doJSON(t, srv, http.MethodPatch, "/api/v1/businesses/"+testBusinessID,
		`{"name":"Concurrent Name"}`, cookie)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d: %s", first.Code, first.Body.String())
	}
	second := doJSON(t, srv, http.MethodPatch, "/api/v1/businesses/"+testBusinessID,
		`{"services":["checkups","screening"]}`, cookie)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d: %s", second.Code, second.Body.String())
	}
	if len(businesses.updated) != 2 || businesses.updated[1].Name != nil ||
		businesses.updated[1].Services == nil {
		t.Fatalf("updates = %+v", businesses.updated)
	}
	var body businessDetailResponse
	if err := json.NewDecoder(second.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Name != "Concurrent Name" {
		t.Fatalf("name = %q, omitted second-patch field was overwritten", body.Name)
	}
}

func TestPatchBusinessRejectsDraftAndInvalidMergedProfile(t *testing.T) {
	draftStore := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusDraft)}
	srv, cookie := newAuthedOnboardingServer(t, draftStore, &fakePlanStore{}, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})
	draft := doJSON(t, srv, http.MethodPatch, "/api/v1/businesses/"+testBusinessID, `{"name":"New"}`, cookie)
	if draft.Code != http.StatusConflict {
		t.Fatalf("draft status = %d, want 409", draft.Code)
	}

	activeStore := &fakeBusinessStore{business: setupBusinessFixture(t, store.BusinessStatusActive)}
	srv, cookie = newAuthedOnboardingServer(t, activeStore, &fakePlanStore{}, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})
	invalid := doJSON(t, srv, http.MethodPatch, "/api/v1/businesses/"+testBusinessID,
		`{"location":{"address":"","area":"","city":"","country":"Singapore"}}`, cookie)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, want 400: %s", invalid.Code, invalid.Body.String())
	}
	if len(activeStore.updated) != 0 {
		t.Fatal("invalid patch reached store")
	}
}

func TestCreateBusinessStartsGeneration(t *testing.T) {
	businesses := &fakeBusinessStore{business: store.Business{ID: mustHashV7(t, testBusinessID)}}
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 20}}
	temporal := &fakeTemporalClient{}
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses", `{"name":"Acme Clinic","website":"acme.example"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
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

func TestCreateBusinessMissingName(t *testing.T) {
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, &fakePlanStore{}, &fakeProposalStore{}, &fakeApplyStore{}, &fakeTemporalClient{})
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses", `{"name":"  ","website":"x.example"}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateBusinessPlanLookupFails(t *testing.T) {
	plans := &fakePlanStore{err: store.ErrNotFound}
	businesses := &fakeBusinessStore{}
	temporal := &fakeTemporalClient{}
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses", `{"name":"Acme"}`, cookie)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// The business must not be created, nor a workflow started, when the plan
	// (which sizes the prompt count) can't be resolved.
	if len(businesses.created) != 0 || len(temporal.started) != 0 {
		t.Fatalf("no create/start expected on plan failure; got created=%d started=%d", len(businesses.created), len(temporal.started))
	}
}

func TestGetProposalReady(t *testing.T) {
	payload := json.RawMessage(`{"low_confidence":true}`)
	proposals := &fakeProposalStore{proposal: store.ProfileProposal{Payload: payload}}
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, &fakePlanStore{}, proposals, &fakeApplyStore{}, &fakeTemporalClient{})

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/businesses/"+testBusinessID+"/proposal", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp proposalStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != proposalStatusReady {
		t.Fatalf("status = %q, want ready", resp.Status)
	}
	if string(resp.Payload) != string(payload) {
		t.Fatalf("payload = %s, want %s", resp.Payload, payload)
	}
}

func TestGetProposalGenerating(t *testing.T) {
	proposals := &fakeProposalStore{getErr: store.ErrNotFound}
	temporal := &fakeTemporalClient{describeStatus: enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING}
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, &fakePlanStore{}, proposals, &fakeApplyStore{}, temporal)

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/businesses/"+testBusinessID+"/proposal", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp proposalStatusResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != proposalStatusGenerating {
		t.Fatalf("status = %q, want generating", resp.Status)
	}
}

func TestGetProposalFailedWhenWorkflowNotFound(t *testing.T) {
	proposals := &fakeProposalStore{getErr: store.ErrNotFound}
	temporal := &fakeTemporalClient{describeErr: serviceerror.NewNotFound("no workflow")}
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, &fakePlanStore{}, proposals, &fakeApplyStore{}, temporal)

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/businesses/"+testBusinessID+"/proposal", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp proposalStatusResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != proposalStatusFailed {
		t.Fatalf("status = %q, want failed", resp.Status)
	}
}

func TestGetProposalNotFoundBusiness(t *testing.T) {
	// No pending proposal and the business is not owned/does not exist → 404.
	proposals := &fakeProposalStore{getErr: store.ErrNotFound}
	businesses := &fakeBusinessStore{getErr: store.ErrNotFound}
	srv, cookie := newAuthedOnboardingServer(t, businesses, &fakePlanStore{}, proposals, &fakeApplyStore{}, &fakeTemporalClient{})

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/businesses/"+testBusinessID+"/proposal", "", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRegenProposalDiscardsAndStarts(t *testing.T) {
	businesses := &fakeBusinessStore{business: store.Business{Status: store.BusinessStatusDraft, Name: "Acme"}}
	proposals := &fakeProposalStore{}
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 20}}
	temporal := &fakeTemporalClient{}
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, proposals, &fakeApplyStore{}, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/proposal/regen", "", cookie)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if proposals.discards != 1 {
		t.Fatalf("DiscardPending calls = %d, want 1", proposals.discards)
	}
	if len(temporal.started) != 1 {
		t.Fatalf("ExecuteWorkflow calls = %d, want 1", len(temporal.started))
	}
}

func TestRegenProposalRejectedWhenNotDraft(t *testing.T) {
	businesses := &fakeBusinessStore{business: store.Business{Status: store.BusinessStatusActive}}
	proposals := &fakeProposalStore{}
	temporal := &fakeTemporalClient{}
	srv, cookie := newAuthedOnboardingServer(t, businesses, &fakePlanStore{}, proposals, &fakeApplyStore{}, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/proposal/regen", "", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	// An active business must not have its (nonexistent) pending proposal touched
	// nor a workflow started.
	if proposals.discards != 0 || len(temporal.started) != 0 {
		t.Fatalf("no discard/start expected; got discards=%d started=%d", proposals.discards, len(temporal.started))
	}
}

func TestRegenProposalAlreadyRunning(t *testing.T) {
	businesses := &fakeBusinessStore{business: store.Business{Status: store.BusinessStatusDraft, Name: "Acme"}}
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 20}}
	temporal := &fakeTemporalClient{execErr: serviceerror.NewWorkflowExecutionAlreadyStarted("already", "", "")}
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, &fakeProposalStore{}, &fakeApplyStore{}, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/proposal/regen", "", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// validApplyPayload is a final review payload that passes ValidateProposal at
// plan.prompt_limit == 4: required profile fields, a two-letter country, four
// prompts covering all four kinds, none naming the business.
const validApplyPayload = `{
  "low_confidence": false,
  "profile": {
    "name": "Acme Clinic",
    "aliases": [],
    "category": "orthopaedic clinic",
    "practitioners": [{"name": "Dr Tan", "role": "surgeon"}],
    "services": ["consultation"],
    "location": {"address": "", "area": "Novena", "city": "Singapore", "country": "SG"}
  },
  "prompts": [
    {"text": "best orthopaedic clinic in Singapore", "kind": "category"},
    {"text": "where can I get ACL reconstruction in Singapore", "kind": "service"},
    {"text": "knee pain that won't go away, who should I see in Singapore", "kind": "condition"},
    {"text": "orthopaedic specialist near Novena", "kind": "location"}
  ]
}`

func applyResult(t *testing.T) store.ApplyProposalResult {
	return store.ApplyProposalResult{Business: store.Business{
		ID:     mustHashV7(t, testBusinessID),
		Name:   "Acme Clinic",
		Status: store.BusinessStatusActive,
	}}
}

func TestApplyBusinessHappyPath(t *testing.T) {
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 4, RunInterval: "weekly"}}
	apply := &fakeApplyStore{result: applyResult(t)}
	temporal := &fakeTemporalClient{}
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/apply", validApplyPayload, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(apply.calls) != 1 {
		t.Fatalf("Apply calls = %d, want 1", len(apply.calls))
	}
	call := apply.calls[0]
	if call.BusinessID.String() != testBusinessID {
		t.Fatalf("apply business id = %s, want %s", call.BusinessID, testBusinessID)
	}
	if call.Category != "orthopaedic clinic" {
		t.Fatalf("apply category = %q", call.Category)
	}
	if len(call.PromptTexts) != 4 {
		t.Fatalf("apply prompt texts = %d, want 4 (kind dropped)", len(call.PromptTexts))
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

func TestApplyBusinessValidationFails(t *testing.T) {
	// plan.prompt_limit 20 but the payload carries 4 prompts → count mismatch.
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 20, RunInterval: "weekly"}}
	apply := &fakeApplyStore{result: applyResult(t)}
	temporal := &fakeTemporalClient{}
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/apply", validApplyPayload, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(apply.calls) != 0 {
		t.Fatalf("Apply calls = %d, want 0 on validation failure", len(apply.calls))
	}
	if len(temporal.started) != 0 {
		t.Fatalf("ExecuteWorkflow calls = %d, want 0", len(temporal.started))
	}
}

func TestApplyBusinessNotDraft(t *testing.T) {
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 4, RunInterval: "weekly"}}
	apply := &fakeApplyStore{err: store.ErrBusinessNotDraft}
	temporal := &fakeTemporalClient{}
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/apply", validApplyPayload, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if len(temporal.started) != 0 {
		t.Fatalf("no first run expected when apply rejects; got %d", len(temporal.started))
	}
}

func TestApplyBusinessNotFound(t *testing.T) {
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 4, RunInterval: "weekly"}}
	apply := &fakeApplyStore{err: store.ErrNotFound}
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, &fakeTemporalClient{})

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/apply", validApplyPayload, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestApplyBusinessScheduleError(t *testing.T) {
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 4, RunInterval: "weekly"}}
	apply := &fakeApplyStore{result: applyResult(t)}
	temporal := &fakeTemporalClient{schedule: &fakeScheduleClient{err: errAny}}
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/apply", validApplyPayload, cookie)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	// The DB apply committed, but the first run must not be started once schedule
	// creation failed.
	if len(temporal.started) != 0 {
		t.Fatalf("no first run expected after schedule failure; got %d", len(temporal.started))
	}
}

func TestApplyBusinessFirstRunAlreadyStarted(t *testing.T) {
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 4, RunInterval: "weekly"}}
	apply := &fakeApplyStore{result: applyResult(t)}
	temporal := &fakeTemporalClient{execErr: serviceerror.NewWorkflowExecutionAlreadyStarted("already", "", "")}
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, plans, &fakeProposalStore{}, apply, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/apply", validApplyPayload, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (already-started first run is idempotent): %s", rec.Code, rec.Body.String())
	}
}

var errAny = errorString("boom")

type errorString string

func (e errorString) Error() string { return string(e) }
