package workflows

import (
	"context"
	"errors"
	"strings"

	"opensight/internal/llm"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// ProposeProfileInput is the ONB-3 proposal request: the user-entered
// name/website plus the two evidence sources (FetchSite text, ResearchBusiness
// summary) and the plan's prompt limit.
type ProposeProfileInput struct {
	Name            string
	Website         string
	SiteText        string
	ResearchSummary string
	PromptLimit     int
}

// ProposeProfileOutput carries the decoded proposal payload for ONB-4 to
// persist. Proposed is false when validation still failed after
// llm.MaxProposeProfileAttempts (mirrors AnalyzeResultOutput.Analyzed) — ONB-4
// must not write a profile_proposals row in that case; treat it like a hard
// generation failure.
type ProposeProfileOutput struct {
	Payload        llm.ProposalPayload
	Model          string
	Proposed       bool
	ValidationErrs []string
}

// ProposeProfile runs the single structured-output proposal call with
// deterministic validation and one validation-retry (design 03 step 3).
//
// Empty name or non-positive PromptLimit is bad input and non-retryable. A
// runner error the runner marks non-retryable (400-class, content-policy
// refusal) won't fix on retry, so it becomes non-retryable too; every other
// error propagates for Temporal's default retry. A proposal that still fails
// validation after the retry returns Proposed=false (not an error) so ONB-4's
// failure posture decides what to do.
func (a *Activities) ProposeProfile(ctx context.Context, in ProposeProfileInput) (ProposeProfileOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return ProposeProfileOutput{}, temporal.NewNonRetryableApplicationError(
			"propose profile", "BadInput", errors.New("business name is required"))
	}
	if in.PromptLimit <= 0 {
		return ProposeProfileOutput{}, temporal.NewNonRetryableApplicationError(
			"propose profile", "BadInput", errors.New("prompt limit must be positive"))
	}

	result, err := llm.ProposeWithRetry(ctx, a.Proposer, llm.ProposeProfileInput{
		Name:            name,
		Website:         in.Website,
		SiteText:        in.SiteText,
		ResearchSummary: in.ResearchSummary,
		PromptLimit:     in.PromptLimit,
	})
	if err != nil {
		if errors.Is(err, llm.ErrNonRetryable) {
			return ProposeProfileOutput{}, temporal.NewNonRetryableApplicationError(
				"propose profile", "ProposalRefused", err)
		}
		return ProposeProfileOutput{}, err
	}

	if !result.Proposed {
		// Never log SiteText, ResearchSummary, or the raw model JSON (PII/scraped
		// content) — only the business name and the deterministic failures.
		activity.GetLogger(ctx).Warn("propose profile: proposal failed validation after retry",
			"business_name", name, "validation_errors", result.ValidationErrs)
	}

	return ProposeProfileOutput{
		Payload:        result.Payload,
		Model:          result.Model,
		Proposed:       result.Proposed,
		ValidationErrs: result.ValidationErrs,
	}, nil
}
