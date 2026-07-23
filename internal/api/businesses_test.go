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

func newAuthedOnboardingServer(t *testing.T, businesses businessStore, plans planStore, proposals proposalStore, temporal temporalClient) (*Server, *http.Cookie) {
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
			temporal:          temporal,
			temporalTaskQueue: "opensight-test",
			secureCookies:     false,
			sessionTTL:        time.Hour,
		},
		&http.Cookie{Name: sessionCookieName, Value: raw}
}

const testBusinessID = "01950000-0000-7000-8000-0000000000c3"

func TestCreateBusinessStartsGeneration(t *testing.T) {
	businesses := &fakeBusinessStore{business: store.Business{ID: mustHashV7(t, testBusinessID)}}
	plans := &fakePlanStore{plan: store.Plan{PromptLimit: 20}}
	temporal := &fakeTemporalClient{}
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, &fakeProposalStore{}, temporal)

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
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, &fakePlanStore{}, &fakeProposalStore{}, &fakeTemporalClient{})
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses", `{"name":"  ","website":"x.example"}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateBusinessPlanLookupFails(t *testing.T) {
	plans := &fakePlanStore{err: store.ErrNotFound}
	businesses := &fakeBusinessStore{}
	temporal := &fakeTemporalClient{}
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, &fakeProposalStore{}, temporal)

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
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, &fakePlanStore{}, proposals, &fakeTemporalClient{})

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
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, &fakePlanStore{}, proposals, temporal)

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
	srv, cookie := newAuthedOnboardingServer(t, &fakeBusinessStore{}, &fakePlanStore{}, proposals, temporal)

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
	srv, cookie := newAuthedOnboardingServer(t, businesses, &fakePlanStore{}, proposals, &fakeTemporalClient{})

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
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, proposals, temporal)

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
	srv, cookie := newAuthedOnboardingServer(t, businesses, &fakePlanStore{}, proposals, temporal)

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
	srv, cookie := newAuthedOnboardingServer(t, businesses, plans, &fakeProposalStore{}, temporal)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/businesses/"+testBusinessID+"/proposal/regen", "", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}
