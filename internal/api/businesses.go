package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"
	"opensight/internal/workflows"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

type createBusinessRequest struct {
	Name    string `json:"name"`
	Website string `json:"website"`
}

type planResponse struct {
	Slug        string   `json:"slug"`
	PromptLimit int      `json:"prompt_limit"`
	RunInterval string   `json:"run_interval"`
	Platforms   []string `json:"platforms"`
}

type businessDetailResponse struct {
	ID       string               `json:"id"`
	Status   string               `json:"status"`
	Name     string               `json:"name"`
	Website  *string              `json:"website"`
	Aliases  []string             `json:"aliases"`
	Category *string              `json:"category"`
	Services []string             `json:"services"`
	Location llm.ProposedLocation `json:"location"`
	Plan     planResponse         `json:"plan"`
}

type patchBusinessRequest struct {
	Name     *string               `json:"name"`
	Website  *string               `json:"website"`
	Aliases  *[]string             `json:"aliases"`
	Category *string               `json:"category"`
	Services *[]string             `json:"services"`
	Location *llm.ProposedLocation `json:"location"`
}

// proposalStatusResponse is the GET/regen proposal body. Status is one of
// "generating", "ready", or "failed"; Payload is present only when ready.
// Stage is the workflow's current generation stage, present only while
// generating (omitted when the stage query fails or degrades).
type proposalStatusResponse struct {
	Status  string          `json:"status"`
	Stage   string          `json:"stage,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

const (
	proposalStatusGenerating = "generating"
	proposalStatusReady      = "ready"
	proposalStatusFailed     = "failed"
)

func (s *Server) handleGetBusiness(w http.ResponseWriter, r *http.Request) {
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "get business: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}
	business, err := s.businesses.GetBusiness(r.Context(), su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	plan, err := s.plans.GetTenantPlan(r.Context(), su.TenantID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	resp, err := businessDetailToResponse(business, plan)
	if err != nil {
		s.writeInternalError(w, "get business: decode profile", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePatchBusiness(w http.ResponseWriter, r *http.Request) {
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "patch business: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}
	current, err := s.businesses.GetBusiness(r.Context(), su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if current.Status != store.BusinessStatusActive {
		writeProblem(w, http.StatusConflict, "conflict", "business profile can only be edited after activation")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req patchBusinessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "bad request", "request body must be valid JSON")
		return
	}
	profile, err := businessToProfile(current)
	if err != nil {
		s.writeInternalError(w, "patch business: decode current profile", err)
		return
	}
	website := current.Website
	if req.Name != nil {
		profile.Name = *req.Name
	}
	if req.Website != nil {
		value := strings.TrimSpace(*req.Website)
		if value == "" {
			website = nil
		} else {
			website = &value
		}
	}
	if req.Aliases != nil {
		profile.Aliases = *req.Aliases
	}
	if req.Category != nil {
		profile.Category = *req.Category
	}
	if req.Services != nil {
		profile.Services = *req.Services
	}
	if req.Location != nil {
		profile.Location = *req.Location
	}
	if errs := llm.ValidateProfile(profile); len(errs) > 0 {
		writeProblem(w, http.StatusBadRequest, "bad request", strings.Join(errs, "; "))
		return
	}
	var services, location *json.RawMessage
	if req.Services != nil {
		value, _ := json.Marshal(profile.Services)
		raw := json.RawMessage(value)
		services = &raw
	}
	if req.Location != nil {
		value, _ := json.Marshal(profile.Location)
		raw := json.RawMessage(value)
		location = &raw
	}
	updated, err := s.businesses.UpdateActiveProfile(r.Context(), store.UpdateBusinessProfileParams{
		TenantID: su.TenantID, BusinessID: businessID, Name: req.Name,
		WebsiteSet: req.Website != nil, Website: website, Aliases: req.Aliases, Category: req.Category,
		Services: services, Location: location,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	plan, err := s.plans.GetTenantPlan(r.Context(), su.TenantID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	resp, err := businessDetailToResponse(updated, plan)
	if err != nil {
		s.writeInternalError(w, "patch business: decode updated profile", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func businessToProfile(b store.Business) (llm.ProposedProfile, error) {
	profile := llm.ProposedProfile{Name: b.Name, Aliases: b.Aliases, Services: []string{}}
	if b.Category != nil {
		profile.Category = *b.Category
	}
	if len(b.Services) > 0 && string(b.Services) != "null" {
		if err := json.Unmarshal(b.Services, &profile.Services); err != nil {
			return llm.ProposedProfile{}, err
		}
	}
	if len(b.Location) > 0 && string(b.Location) != "null" {
		if err := json.Unmarshal(b.Location, &profile.Location); err != nil {
			return llm.ProposedProfile{}, err
		}
	}
	return profile, nil
}

func businessDetailToResponse(b store.Business, plan store.Plan) (businessDetailResponse, error) {
	profile, err := businessToProfile(b)
	if err != nil {
		return businessDetailResponse{}, err
	}
	return businessDetailResponse{
		ID: b.ID.String(), Status: string(b.Status), Name: profile.Name,
		Website: b.Website, Aliases: profile.Aliases, Category: b.Category,
		Services: profile.Services, Location: profile.Location,
		Plan: planResponse{Slug: plan.Slug, PromptLimit: plan.PromptLimit, RunInterval: plan.RunInterval, Platforms: plan.Platforms},
	}, nil
}

// handleCreateBusiness inserts a draft business and starts
// GenerateProfileWorkflow (design 03). The generated prompt count comes from the
// tenant's plan, never a hardcoded 20. Generation writes only profile_proposals;
// the businesses row is created here in draft and is never touched by the
// workflow (the 02 invariant).
func (s *Server) handleCreateBusiness(w http.ResponseWriter, r *http.Request) {
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "create business: missing session context", errors.New("missing session context"))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req createBusinessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "bad request", "request body must be valid JSON")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeProblem(w, http.StatusBadRequest, "bad request", "name is required")
		return
	}
	website := strings.TrimSpace(req.Website)

	ctx := r.Context()
	plan, err := s.plans.GetTenantPlan(ctx, su.TenantID)
	if err != nil {
		s.writeInternalError(w, "create business: get tenant plan", err)
		return
	}

	params := store.CreateBusinessParams{
		TenantID: su.TenantID,
		Status:   store.BusinessStatusDraft,
		Name:     name,
	}
	if website != "" {
		params.Website = &website
	}
	business, err := s.businesses.CreateBusiness(ctx, params)
	if err != nil {
		s.writeInternalError(w, "create business: insert", err)
		return
	}

	if err := s.startGeneration(ctx, su.TenantID, business.ID, name, website, plan.PromptLimit); err != nil {
		// The draft row survives (no compensation): it is recoverable via /me,
		// and GET .../proposal reports "failed" since no workflow is running.
		s.writeInternalError(w, "create business: start generation", err)
		return
	}

	writeJSON(w, http.StatusCreated, businessResponse{
		ID:     business.ID.String(),
		Name:   business.Name,
		Status: string(business.Status),
	})
}

// handleGetProposal reports generation status and, when ready, the pending
// proposal payload (design 03). A pending row means ready; otherwise the
// business's GenerateProfileWorkflow is consulted for generating vs. failed.
func (s *Server) handleGetProposal(w http.ResponseWriter, r *http.Request) {
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "get proposal: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}

	ctx := r.Context()
	proposal, err := s.proposals.GetPending(ctx, su.TenantID, businessID)
	if err == nil {
		writeJSON(w, http.StatusOK, proposalStatusResponse{
			Status:  proposalStatusReady,
			Payload: proposal.Payload,
		})
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		s.writeInternalError(w, "get proposal: get pending", err)
		return
	}

	// GetPending's ErrNotFound is ambiguous (missing/foreign business OR no
	// pending row). Confirm ownership so a bad id 404s instead of masquerading
	// as generating.
	if _, err := s.businesses.GetBusiness(ctx, su.TenantID, businessID); err != nil {
		writeStoreError(w, err)
		return
	}

	status, stage, err := s.generationStatus(ctx, businessID)
	if err != nil {
		s.writeInternalError(w, "get proposal: describe generation", err)
		return
	}
	writeJSON(w, http.StatusOK, proposalStatusResponse{Status: status, Stage: stage})
}

// handleRegenProposal discards the pending proposal and re-runs generation
// (design 03). Only allowed while the business is draft; after activation,
// profile changes are manual edits in Setup.
func (s *Server) handleRegenProposal(w http.ResponseWriter, r *http.Request) {
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "regen proposal: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}

	ctx := r.Context()
	business, err := s.businesses.GetBusiness(ctx, su.TenantID, businessID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if business.Status != store.BusinessStatusDraft {
		writeProblem(w, http.StatusConflict, "conflict",
			"proposal can only be regenerated while the business is in draft")
		return
	}

	if err := s.proposals.DiscardPending(ctx, su.TenantID, businessID); err != nil {
		s.writeInternalError(w, "regen proposal: discard pending", err)
		return
	}

	plan, err := s.plans.GetTenantPlan(ctx, su.TenantID)
	if err != nil {
		s.writeInternalError(w, "regen proposal: get tenant plan", err)
		return
	}

	if err := s.startGeneration(ctx, su.TenantID, businessID, business.Name, websiteOrEmpty(business.Website), plan.PromptLimit); err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &alreadyStarted) {
			writeProblem(w, http.StatusConflict, "conflict", "profile generation is already in progress")
			return
		}
		s.writeInternalError(w, "regen proposal: start generation", err)
		return
	}
	writeJSON(w, http.StatusAccepted, proposalStatusResponse{Status: proposalStatusGenerating})
}

// handleApplyBusiness is the apply transaction plus first run (ONB-6, design 03
// "Review and apply"): it takes the final user-edited payload verbatim, activates
// the business and inserts its prompts in one DB transaction (the only path that
// writes profile values to businesses), then creates the weekly monitoring
// Schedule and triggers the first run now with trigger=initial. The DB tx and
// the Temporal calls are not atomic with each other (design 03): if the tx
// commits but a Temporal call fails, the business is active and a client retry
// gets 409 — an accepted MVP gap, mitigated by the idempotent schedule/run
// creation for any manual recovery.
func (s *Server) handleApplyBusiness(w http.ResponseWriter, r *http.Request) {
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "apply business: missing session context", errors.New("missing session context"))
		return
	}
	businessID, ok := pathID(w, r, "businessID")
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var payload llm.ProposalPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeProblem(w, http.StatusBadRequest, "bad request", "request body must be valid JSON")
		return
	}

	ctx := r.Context()
	plan, err := s.plans.GetTenantPlan(ctx, su.TenantID)
	if err != nil {
		s.writeInternalError(w, "apply business: get tenant plan", err)
		return
	}

	if errs := llm.ValidateProposal(payload, llm.ProposeProfileInput{
		Name:        payload.Profile.Name,
		PromptLimit: plan.PromptLimit,
	}); len(errs) > 0 {
		writeProblem(w, http.StatusBadRequest, "bad request", strings.Join(errs, "; "))
		return
	}

	services, err := json.Marshal(payload.Profile.Services)
	if err != nil {
		s.writeInternalError(w, "apply business: marshal services", err)
		return
	}
	location, err := json.Marshal(payload.Profile.Location)
	if err != nil {
		s.writeInternalError(w, "apply business: marshal location", err)
		return
	}

	promptTexts := make([]string, 0, len(payload.Prompts))
	for _, p := range payload.Prompts {
		// prompt kind is UI-only; prompts has no kind column, so drop it.
		promptTexts = append(promptTexts, p.Text)
	}

	now := nowUTC()
	result, err := s.apply.Apply(ctx, store.ApplyProposalParams{
		TenantID:      su.TenantID,
		BusinessID:    businessID,
		Name:          payload.Profile.Name,
		Aliases:       payload.Profile.Aliases,
		Category:      payload.Profile.Category,
		Services:      services,
		Location:      location,
		PromptTexts:   promptTexts,
		ActivatedAt:   now,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeStoreError(w, err)
		case errors.Is(err, store.ErrBusinessNotDraft):
			writeProblem(w, http.StatusConflict, "conflict", "business is already active")
		case errors.Is(err, store.ErrPromptLimitExceeded):
			writeProblem(w, http.StatusBadRequest, "bad request", "prompt count exceeds the plan limit")
		default:
			s.writeInternalError(w, "apply business: apply proposal", err)
		}
		return
	}

	if _, err := workflows.CreateMonitorSchedule(ctx, s.temporal, workflows.CreateScheduleParams{
		BusinessID:  businessID,
		Platform:    store.PlatformChatGPT,
		RunInterval: plan.RunInterval,
		TaskQueue:   s.temporalTaskQueue,
	}); err != nil {
		s.writeInternalError(w, "apply business: create schedule", err)
		return
	}

	scheduledFor := workflows.TruncateToDay(now)
	workflowID := workflows.RunWorkflowID(businessID, store.PlatformChatGPT, scheduledFor)
	if _, err := s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: s.temporalTaskQueue,
	}, workflows.RunWorkflow, workflows.RunWorkflowInput{
		BusinessID:   businessID,
		Platform:     store.PlatformChatGPT,
		ScheduledFor: scheduledFor,
		Trigger:      store.RunTriggerInitial,
	}); err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if !errors.As(err, &alreadyStarted) {
			s.writeInternalError(w, "apply business: start first run", err)
			return
		}
	}

	writeJSON(w, http.StatusOK, businessResponse{
		ID:     result.Business.ID.String(),
		Name:   result.Business.Name,
		Status: string(result.Business.Status),
	})
}

// startGeneration starts GenerateProfileWorkflow under the deterministic
// per-business workflow id, so a regen while a prior run is still open surfaces
// as WorkflowExecutionAlreadyStarted (409) rather than a duplicate run.
func (s *Server) startGeneration(ctx context.Context, tenantID, businessID domain.ID, name, website string, promptLimit int) error {
	_, err := s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflows.GenerateProfileWorkflowID(businessID),
		TaskQueue: s.temporalTaskQueue,
	}, workflows.GenerateProfileWorkflow, workflows.GenerateProfileWorkflowInput{
		TenantID:    tenantID,
		BusinessID:  businessID,
		Name:        name,
		Website:     website,
		PromptLimit: promptLimit,
	})
	return err
}

// generationStatus maps the business's GenerateProfileWorkflow execution state
// to a proposal status when there is no pending proposal row. A not-found
// workflow (never started or history expired) and any terminal state both mean
// failed; only a running workflow means generating. For a running workflow it
// also queries the workflow's current stage; any query error degrades to an
// empty stage (still generating) rather than failing the poll — a briefly
// unavailable worker, a pre-deploy run without the handler, or a Describe/Query
// race on a just-closed workflow must not break status reporting.
func (s *Server) generationStatus(ctx context.Context, businessID domain.ID) (status, stage string, err error) {
	workflowID := workflows.GenerateProfileWorkflowID(businessID)
	desc, err := s.temporal.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return proposalStatusFailed, "", nil
		}
		return "", "", err
	}
	if desc.GetWorkflowExecutionInfo().GetStatus() != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return proposalStatusFailed, "", nil
	}
	return proposalStatusGenerating, s.generationStage(ctx, workflowID), nil
}

// generationStage queries the running workflow for its current stage, returning
// an empty string on any error (see generationStatus). The query is given a
// short deadline so a slow or unreachable worker cannot stall the poll.
func (s *Server) generationStage(ctx context.Context, workflowID string) string {
	qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := s.temporal.QueryWorkflow(qctx, workflowID, "", workflows.GenerationStageQuery)
	if err != nil {
		return ""
	}
	var stage string
	if err := resp.Get(&stage); err != nil {
		return ""
	}
	return stage
}

func websiteOrEmpty(website *string) string {
	if website == nil {
		return ""
	}
	return *website
}
