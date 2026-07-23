package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"opensight/internal/domain"
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

// proposalStatusResponse is the GET/regen proposal body. Status is one of
// "generating", "ready", or "failed"; Payload is present only when ready.
type proposalStatusResponse struct {
	Status  string          `json:"status"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

const (
	proposalStatusGenerating = "generating"
	proposalStatusReady      = "ready"
	proposalStatusFailed     = "failed"
)

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

	status, err := s.generationStatus(ctx, businessID)
	if err != nil {
		s.writeInternalError(w, "get proposal: describe generation", err)
		return
	}
	writeJSON(w, http.StatusOK, proposalStatusResponse{Status: status})
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
// failed; only a running workflow means generating.
func (s *Server) generationStatus(ctx context.Context, businessID domain.ID) (string, error) {
	desc, err := s.temporal.DescribeWorkflowExecution(ctx, workflows.GenerateProfileWorkflowID(businessID), "")
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return proposalStatusFailed, nil
		}
		return "", err
	}
	if desc.GetWorkflowExecutionInfo().GetStatus() == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return proposalStatusGenerating, nil
	}
	return proposalStatusFailed, nil
}

func websiteOrEmpty(website *string) string {
	if website == nil {
		return ""
	}
	return *website
}
