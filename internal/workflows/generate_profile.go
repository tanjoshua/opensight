package workflows

import (
	"fmt"
	"time"

	"opensight/internal/domain"
	"opensight/internal/llm"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// Temporal-level activity retry budgets. These are distinct from
	// llm.MaxProposeProfileAttempts, which is the in-activity validation retry.
	maxFetchSiteActivityAttempts        = 2
	maxResearchBusinessActivityAttempts = 2
	maxProposeProfileActivityAttempts   = 2
)

// onboardingResearchLocationHint is a search-context hint ONLY (never
// persisted) for ResearchBusiness's web_search call, which requires a country
// to validate. At business-creation time no location is known yet — discovering
// it is this workflow's job — so we seed the search with the PRD's Singapore-only
// MVP market. This is not the persisted businesses.location (that has no market
// default and is set only at apply time from the reviewed proposal).
var onboardingResearchLocationHint = llm.Location{Country: "SG"}

// GenerateProfileWorkflowID is the deterministic workflow id per business
// (design 03). A regen after the first run has closed reuses it via Temporal's
// default id-reuse policy; a start while the prior run is still open fails with
// WorkflowExecutionAlreadyStarted, which the API surfaces as 409.
func GenerateProfileWorkflowID(businessID domain.ID) string {
	return fmt.Sprintf("generate-profile-%s", businessID)
}

// GenerateProfileWorkflowInput starts profile generation for a freshly created
// draft business. PromptLimit is plan.prompt_limit, resolved by the caller (the
// count is never hardcoded — design 03).
type GenerateProfileWorkflowInput struct {
	TenantID    domain.ID
	BusinessID  domain.ID
	Name        string
	Website     string
	PromptLimit int
}

// GenerateProfileWorkflow chains FetchSite -> ResearchBusiness -> ProposeProfile
// -> PersistProposal (design 03). Failure posture: either evidence source alone
// is enough to proceed; FetchSite failing specifically forces low_confidence on
// the eventual proposal so the UI nudges harder review. Only if both sources
// fail, or ProposeProfile still fails validation, does the workflow fail — the
// UI then offers manual setup.
func GenerateProfileWorkflow(ctx workflow.Context, input GenerateProfileWorkflowInput) error {
	fetchCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 45 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 5 * time.Second,
			MaximumAttempts: maxFetchSiteActivityAttempts,
		},
	})
	var fetchOut FetchSiteOutput
	fetchErr := workflow.ExecuteActivity(fetchCtx, acts.FetchSite, FetchSiteInput{
		Website: input.Website,
	}).Get(ctx, &fetchOut)
	siteText := ""
	fetchFailed := fetchErr != nil
	if fetchFailed {
		workflow.GetLogger(ctx).Warn("fetch site failed; proceeding research-only",
			"business_id", input.BusinessID.String(), "error", fetchErr.Error())
	} else {
		siteText = fetchOut.Text
	}

	researchCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 10 * time.Second,
			MaximumAttempts: maxResearchBusinessActivityAttempts,
		},
	})
	var researchOut ResearchBusinessOutput
	researchErr := workflow.ExecuteActivity(researchCtx, acts.ResearchBusiness, ResearchBusinessInput{
		Name:     input.Name,
		Location: onboardingResearchLocationHint,
	}).Get(ctx, &researchOut)
	researchSummary := ""
	if researchErr != nil {
		workflow.GetLogger(ctx).Warn("research business failed; proceeding site-only",
			"business_id", input.BusinessID.String(), "error", researchErr.Error())
	} else {
		researchSummary = researchOut.Summary
	}

	if fetchFailed && researchErr != nil {
		return temporal.NewApplicationError(
			"both site fetch and business research failed", "GenerationFailed")
	}

	proposeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 4 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 10 * time.Second,
			MaximumAttempts: maxProposeProfileActivityAttempts,
		},
	})
	var proposeOut ProposeProfileOutput
	if err := workflow.ExecuteActivity(proposeCtx, acts.ProposeProfile, ProposeProfileInput{
		Name:            input.Name,
		Website:         input.Website,
		SiteText:        siteText,
		ResearchSummary: researchSummary,
		PromptLimit:     input.PromptLimit,
	}).Get(ctx, &proposeOut); err != nil {
		return err
	}
	if !proposeOut.Proposed {
		return temporal.NewApplicationError(
			"proposal failed validation after retry", "ProposalInvalid")
	}

	payload := proposeOut.Payload
	if fetchFailed {
		payload.LowConfidence = true
	}

	persistCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Second,
	})
	return workflow.ExecuteActivity(persistCtx, acts.PersistProposal, PersistProposalInput{
		TenantID:   input.TenantID,
		BusinessID: input.BusinessID,
		Payload:    payload,
	}).Get(ctx, nil)
}
