package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/llm"
	"opensight/internal/store"
	"opensight/internal/workflows"

	connect "connectrpc.com/connect"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

const (
	proposalStatusGenerating = "generating"
	proposalStatusReady      = "ready"
	proposalStatusFailed     = "failed"
)

var _ opensightv1connect.BusinessServiceHandler = (*Server)(nil)

// CreateBusiness inserts a draft business and starts GenerateProfileWorkflow
// (design 03).
func (s *Server) CreateBusiness(ctx context.Context, req *connect.Request[opensightv1.CreateBusinessRequest]) (*connect.Response[opensightv1.CreateBusinessResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "create business")
	if cerr != nil {
		return nil, cerr
	}

	name := strings.TrimSpace(req.Msg.Name)
	if name == "" {
		return nil, rpcInvalidArgument("name is required")
	}
	website := strings.TrimSpace(req.Msg.Website)

	params := store.CreateBusinessParams{
		AccountID: su.AccountID,
		Status:    store.BusinessStatusDraft,
		Name:      name,
	}
	if website != "" {
		params.Website = &website
	}
	business, err := s.store.CreateBusiness(ctx, params)
	if err != nil {
		return nil, s.rpcInternal("create business: insert", err)
	}

	if err := s.startGeneration(ctx, su.AccountID, business.ID, name, website); err != nil {
		// The draft row survives (no compensation): it is recoverable via /me,
		// and GetProposal reports "failed" since no workflow is running.
		return nil, s.rpcInternal("create business: start generation", err)
	}

	return connect.NewResponse(&opensightv1.CreateBusinessResponse{
		Business: &opensightv1.BusinessSummary{
			Id:     business.ID.String(),
			Name:   business.Name,
			Status: businessStatusToProto(business.Status),
		},
	}), nil
}

// GetBusiness returns the full business profile and plan.
func (s *Server) GetBusiness(ctx context.Context, req *connect.Request[opensightv1.GetBusinessRequest]) (*connect.Response[opensightv1.GetBusinessResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get business")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	business, err := s.store.GetBusiness(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("get business", err)
	}
	resp, err := businessProfileToProto(business)
	if err != nil {
		return nil, s.rpcInternal("get business: decode profile", err)
	}
	return connect.NewResponse(&opensightv1.GetBusinessResponse{Business: resp}), nil
}

// UpdateBusiness merges the request's present fields into the current active
// profile and writes only the changed columns.
func (s *Server) UpdateBusiness(ctx context.Context, req *connect.Request[opensightv1.UpdateBusinessRequest]) (*connect.Response[opensightv1.UpdateBusinessResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "update business")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	current, err := s.store.GetBusiness(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("update business", err)
	}
	if current.Status != store.BusinessStatusActive {
		return nil, rpcFailedPrecondition("business profile can only be edited after activation")
	}

	profile, err := businessToProfile(current)
	if err != nil {
		return nil, s.rpcInternal("update business: decode current profile", err)
	}

	website := current.Website
	if req.Msg.Website != nil {
		value := strings.TrimSpace(*req.Msg.Website)
		if value == "" {
			website = nil
		} else {
			website = &value
		}
	}

	if req.Msg.Name != nil {
		profile.Name = *req.Msg.Name
	}

	var aliasesParam *[]string
	if req.Msg.Aliases != nil {
		aliases := req.Msg.Aliases.GetValues()
		if aliases == nil {
			aliases = []string{}
		}
		profile.Aliases = aliases
		aliasesParam = &aliases
	}

	if req.Msg.Category != nil {
		profile.Category = *req.Msg.Category
	}

	if req.Msg.Services != nil {
		services := req.Msg.Services.GetValues()
		if services == nil {
			services = []string{}
		}
		profile.Services = services
	}

	if req.Msg.Location != nil {
		profile.Location = locationFromProto(req.Msg.Location)
	}

	if errs := llm.ValidateProfile(profile); len(errs) > 0 {
		return nil, rpcInvalidArgument(strings.Join(errs, "; "))
	}

	var services, location *json.RawMessage
	if req.Msg.Services != nil {
		value, _ := json.Marshal(profile.Services)
		raw := json.RawMessage(value)
		services = &raw
	}
	if req.Msg.Location != nil {
		value, _ := json.Marshal(profile.Location)
		raw := json.RawMessage(value)
		location = &raw
	}

	updated, err := s.store.UpdateActiveProfile(ctx, store.UpdateBusinessProfileParams{
		AccountID: su.AccountID, BusinessID: businessID, Name: req.Msg.Name,
		WebsiteSet: req.Msg.Website != nil, Website: website, Aliases: aliasesParam, Category: req.Msg.Category,
		Services: services, Location: location,
	})
	if err != nil {
		return nil, s.rpcError("update business", err)
	}

	resp, err := businessProfileToProto(updated)
	if err != nil {
		return nil, s.rpcInternal("update business: decode updated profile", err)
	}
	return connect.NewResponse(&opensightv1.UpdateBusinessResponse{Business: resp}), nil
}

// GetProposal reports generation status and, when ready, the pending
// proposal payload (design 03).
func (s *Server) GetProposal(ctx context.Context, req *connect.Request[opensightv1.GetProposalRequest]) (*connect.Response[opensightv1.GetProposalResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "get proposal")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	proposal, err := s.store.GetPending(ctx, su.AccountID, businessID)
	if err == nil {
		payload, derr := llm.DecodeProposalPayload(proposal.Payload)
		if derr != nil {
			return nil, s.rpcInternal("get proposal: decode pending payload", derr)
		}
		return connect.NewResponse(&opensightv1.GetProposalResponse{State: &opensightv1.ProposalState{
			Status:  opensightv1.ProposalStatus_PROPOSAL_STATUS_READY,
			Payload: proposalPayloadToProto(payload),
		}}), nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, s.rpcInternal("get proposal: get pending", err)
	}

	// GetPending's ErrNotFound is ambiguous (missing/foreign business OR no
	// pending row). Confirm ownership so a bad id 404s instead of masquerading
	// as generating.
	if _, err := s.store.GetBusiness(ctx, su.AccountID, businessID); err != nil {
		return nil, s.rpcError("get proposal", err)
	}

	status, stage, err := s.generationStatus(ctx, businessID)
	if err != nil {
		return nil, s.rpcInternal("get proposal: describe generation", err)
	}
	return connect.NewResponse(&opensightv1.GetProposalResponse{State: &opensightv1.ProposalState{
		Status: proposalStatusToProto(status),
		Stage:  generationStageToProto(stage),
	}}), nil
}

// RegenerateProposal discards the pending proposal and re-runs generation
// (design 03).
func (s *Server) RegenerateProposal(ctx context.Context, req *connect.Request[opensightv1.RegenerateProposalRequest]) (*connect.Response[opensightv1.RegenerateProposalResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "regen proposal")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	business, err := s.store.GetBusiness(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("regen proposal", err)
	}
	if business.Status != store.BusinessStatusDraft {
		return nil, rpcFailedPrecondition("proposal can only be regenerated while the business is in draft")
	}
	if req.Msg.Website != nil {
		value := strings.TrimSpace(*req.Msg.Website)
		var website *string
		if value != "" {
			website = &value
		}
		business, err = s.store.UpdateDraftWebsite(ctx, su.AccountID, businessID, website)
		if err != nil {
			return nil, s.rpcError("regen proposal: update website", err)
		}
	}

	// Editing the website is available while generation is in flight. Stop the
	// old run before starting the replacement under the same deterministic ID.
	// Closed or just-finished runs cannot be terminated and need no action.
	if err := s.temporal.TerminateWorkflow(ctx, workflows.GenerateProfileWorkflowID(businessID), "", "research inputs changed"); err != nil {
		var notFound *serviceerror.NotFound
		var failedPrecondition *serviceerror.FailedPrecondition
		if !errors.As(err, &notFound) && !errors.As(err, &failedPrecondition) {
			return nil, s.rpcInternal("regen proposal: stop current generation", err)
		}
	}

	if err := s.store.DiscardPending(ctx, su.AccountID, businessID); err != nil {
		return nil, s.rpcInternal("regen proposal: discard pending", err)
	}

	if err := s.startGeneration(ctx, su.AccountID, businessID, business.Name, websiteOrEmpty(business.Website)); err != nil {
		return nil, s.rpcError("regen proposal: start generation", err)
	}
	return connect.NewResponse(&opensightv1.RegenerateProposalResponse{State: &opensightv1.ProposalState{
		Status: opensightv1.ProposalStatus_PROPOSAL_STATUS_GENERATING,
	}}), nil
}

// ApplyProposal is the apply transaction plus first run (design 03
// "Review and apply"): it takes the final user-edited payload verbatim, activates
// the business and inserts its prompts in one DB transaction (the only path that
// writes profile values to businesses), then creates the weekly monitoring
// Schedule and triggers the first run now with trigger=initial. The DB tx and
// the Temporal calls are not atomic with each other (design 03): if the tx
// commits but a Temporal call fails, the business is active and a client retry
// gets FailedPrecondition (ErrBusinessNotDraft) — an accepted MVP gap,
// mitigated by the idempotent schedule/run creation for any manual recovery.
func (s *Server) ApplyProposal(ctx context.Context, req *connect.Request[opensightv1.ApplyProposalRequest]) (*connect.Response[opensightv1.ApplyProposalResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "apply business")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	payload := proposalPayloadFromProto(req.Msg.Payload)

	plan, err := billing.PlanFor(su.PlanCode)
	if err != nil {
		return nil, s.rpcInternal("apply business: resolve plan", err)
	}

	// Defense in depth: the review screen already gates Approve on these same
	// rules (validateFinalPayload), but the server never trusts the client's
	// prompt count or name-leakage checks — apply is the only path that
	// persists prompts, so it revalidates the submitted payload itself.
	if errs := llm.ValidateProfile(payload.Profile); len(errs) > 0 {
		return nil, rpcInvalidArgument(strings.Join(errs, "; "))
	}
	if errs := llm.ValidateQuestions(payload.Prompts, llm.QuestionsInput{
		Name:        payload.Profile.Name,
		Aliases:     payload.Profile.Aliases,
		PromptLimit: plan.PromptLimit,
	}); len(errs) > 0 {
		return nil, rpcInvalidArgument(strings.Join(errs, "; "))
	}

	services, err := json.Marshal(payload.Profile.Services)
	if err != nil {
		return nil, s.rpcInternal("apply business: marshal services", err)
	}
	location, err := json.Marshal(payload.Profile.Location)
	if err != nil {
		return nil, s.rpcInternal("apply business: marshal location", err)
	}

	promptTexts := make([]string, 0, len(payload.Prompts))
	for _, p := range payload.Prompts {
		promptTexts = append(promptTexts, p.Text)
	}

	now := nowUTC()
	result, err := s.store.Apply(ctx, store.ApplyProposalParams{
		AccountID:   su.AccountID,
		BusinessID:  businessID,
		Name:        payload.Profile.Name,
		Aliases:     payload.Profile.Aliases,
		Category:    payload.Profile.Category,
		Services:    services,
		Location:    location,
		PromptTexts: promptTexts,
		ActivatedAt: now,
	})
	if err != nil {
		return nil, s.rpcError("apply business: apply proposal", err)
	}

	if _, err := workflows.CreateMonitorSchedule(ctx, s.temporal, workflows.CreateScheduleParams{
		BusinessID:  businessID,
		Platform:    store.PlatformChatGPT,
		RunInterval: plan.RunInterval,
		TaskQueue:   s.temporalTaskQueue,
	}); err != nil {
		return nil, s.rpcInternal("apply business: create schedule", err)
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
			return nil, s.rpcInternal("apply business: start first run", err)
		}
	}

	return connect.NewResponse(&opensightv1.ApplyProposalResponse{Business: &opensightv1.BusinessSummary{
		Id:     result.Business.ID.String(),
		Name:   result.Business.Name,
		Status: businessStatusToProto(result.Business.Status),
	}}), nil
}

// GenerateQuestions generates customer questions on demand once the user has
// confirmed the profile through the Services review step (design 03): unlike
// ProposeProfile it does no research, drafting purely from the submitted
// category/services/city. It persists nothing — the generated prompts are
// returned for the client's in-memory draft, same as every other value on the
// review screen.
func (s *Server) GenerateQuestions(ctx context.Context, req *connect.Request[opensightv1.GenerateQuestionsRequest]) (*connect.Response[opensightv1.GenerateQuestionsResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "generate questions")
	if cerr != nil {
		return nil, cerr
	}
	businessID, cerr := rpcID("business_id", req.Msg.BusinessId)
	if cerr != nil {
		return nil, cerr
	}

	business, err := s.store.GetBusiness(ctx, su.AccountID, businessID)
	if err != nil {
		return nil, s.rpcError("generate questions", err)
	}
	if business.Status != store.BusinessStatusDraft {
		return nil, rpcFailedPrecondition("questions can only be generated while the business is in draft")
	}

	profile := proposedProfileFromProto(req.Msg.Profile)
	if errs := llm.ValidateProfile(profile); len(errs) > 0 {
		return nil, rpcInvalidArgument(strings.Join(errs, "; "))
	}

	plan, err := billing.PlanFor(su.PlanCode)
	if err != nil {
		return nil, s.rpcInternal("generate questions: resolve plan", err)
	}

	result, err := llm.GenerateQuestionsWithRetry(ctx, s.questions, llm.QuestionsInput{
		Name:        profile.Name,
		Aliases:     profile.Aliases,
		Category:    profile.Category,
		Services:    profile.Services,
		City:        profile.Location.City,
		PromptLimit: plan.PromptLimit,
	})
	if err != nil {
		return nil, s.rpcInternal("generate questions: run", err)
	}
	if !result.Generated {
		return nil, s.rpcInternal("generate questions: failed validation after retry",
			errors.New(strings.Join(result.ValidationErrs, "; ")))
	}

	prompts := make([]*opensightv1.ProposedPrompt, len(result.Prompts))
	for i, p := range result.Prompts {
		prompts[i] = &opensightv1.ProposedPrompt{Text: p.Text}
	}
	return connect.NewResponse(&opensightv1.GenerateQuestionsResponse{Prompts: prompts}), nil
}

// startGeneration starts GenerateProfileWorkflow under the deterministic
// per-business workflow id, so a regen while a prior run is still open surfaces
// as WorkflowExecutionAlreadyStarted (CodeAlreadyExists) rather than a
// duplicate run.
func (s *Server) startGeneration(ctx context.Context, accountID, businessID domain.ID, name, website string) error {
	_, err := s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflows.GenerateProfileWorkflowID(businessID),
		TaskQueue: s.temporalTaskQueue,
	}, workflows.GenerateProfileWorkflow, workflows.GenerateProfileWorkflowInput{
		AccountID:  accountID,
		BusinessID: businessID,
		Name:       name,
		Website:    website,
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
