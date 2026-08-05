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
	maxFetchSiteActivityAttempts      = 2
	maxProposeProfileActivityAttempts = 2
)

// GenerationStageQuery is the Temporal query name for GenerateProfileWorkflow's
// current stage. The API polls it while a run is still generating to drive the
// onboarding progress step list; the stage only advances between activity
// futures, so a retried activity never regresses or flickers it.
const GenerationStageQuery = "generation-stage"

const (
	GenerationStageFetchingSite = "fetching_site"
	GenerationStageDrafting     = "drafting"
)

// onboardingResearchLocationHint is a search-context hint ONLY (never
// persisted) for the ResearchAndPropose call's web_search, which requires a
// country to validate. At business-creation time no location is known yet —
// discovering it is this workflow's job — so we seed the search with the PRD's
// Singapore-only MVP market. This is not the persisted businesses.location (that
// has no market default and is set only at apply time from the reviewed
// proposal).
var onboardingResearchLocationHint = llm.Location{Country: "SG"}

// GenerateProfileWorkflowID is the deterministic workflow id per business
// (design 03). A regen after the first run has closed reuses it via Temporal's
// default id-reuse policy; a start while the prior run is still open fails with
// WorkflowExecutionAlreadyStarted, which the API surfaces as 409.
func GenerateProfileWorkflowID(businessID domain.ID) string {
	return fmt.Sprintf("generate-profile-%s", businessID)
}

// GenerateProfileWorkflowInput starts profile generation for a freshly created
// draft business. Customer questions are generated separately, on demand, by
// GenerateQuestions once the user reviews Services (design 03) — this input
// carries no prompt limit.
type GenerateProfileWorkflowInput struct {
	AccountID  domain.ID `json:"TenantID"`
	BusinessID domain.ID
	Name       string
	Website    string
}

// GenerateProfileWorkflow chains FetchSite -> ProposeProfile -> PersistProposal
// (design 03). ProposeProfile is the combined research+draft call: it does its
// own web_search (opening pages, including the site itself). FetchSite is the
// free, deterministic primary evidence source but does not gate success on its
// own — its text feeds the combined call and, on failure, the model still has
// web research. Failure posture: FetchSite failing forces low_confidence UNLESS
// the model read the site itself (an open_page on its own domain), in which case
// its own low_confidence judgement stands. Only if ProposeProfile fails outright
// or never validates does the workflow fail — the UI then offers manual setup.
func GenerateProfileWorkflow(ctx workflow.Context, input GenerateProfileWorkflowInput) error {
	stage := GenerationStageFetchingSite
	if err := workflow.SetQueryHandler(ctx, GenerationStageQuery, func() (string, error) {
		return stage, nil
	}); err != nil {
		return err
	}

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

	stage = GenerationStageDrafting
	// ~10 min: the combined call does multiple search/open_page actions plus
	// reasoning plus a possible in-activity validation retry — longer than the old
	// pure structured-output call.
	proposeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: 10 * time.Second,
			MaximumAttempts: maxProposeProfileActivityAttempts,
		},
	})
	var proposeOut ProposeProfileOutput
	if err := workflow.ExecuteActivity(proposeCtx, acts.ProposeProfile, ProposeProfileInput{
		Name:     input.Name,
		Website:  input.Website,
		SiteText: siteText,
		Location: onboardingResearchLocationHint,
	}).Get(ctx, &proposeOut); err != nil {
		return err
	}
	if !proposeOut.Proposed {
		return temporal.NewApplicationError(
			"proposal failed validation after retry", "ProposalInvalid")
	}

	payload := proposeOut.Payload
	// FetchSite failing forces low_confidence, unless the model read the site
	// itself (open_page on its own domain) — then trust its own judgement.
	if fetchFailed && !proposeOut.OpenedOwnSite {
		payload.LowConfidence = true
	}

	persistCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Second,
	})
	return workflow.ExecuteActivity(persistCtx, acts.PersistProposal, PersistProposalInput{
		AccountID:  input.AccountID,
		BusinessID: input.BusinessID,
		Payload:    payload,
	}).Get(ctx, nil)
}
