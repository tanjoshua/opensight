package workflows

import (
	"context"
	"errors"
	"strings"

	"opensight/internal/llm"

	"go.temporal.io/sdk/temporal"
)

// ResearchBusinessInput is the ONB-2 business research request: the user-entered
// business name plus location hints for the web_search call.
type ResearchBusinessInput struct {
	Name     string
	Location llm.Location
}

// ResearchBusinessOutput is the free-text research summary ONB-3 (ProposeProfile)
// feeds into its structured-output call, plus the reporting model for debugging.
// The activity deliberately does not parse the summary into aliases/practitioners
// — that structuring is ONB-3's job.
type ResearchBusinessOutput struct {
	Summary string `json:"summary"`
	Model   string `json:"model"`
}

// ResearchBusiness runs a single OpenAI web_search over the business name and
// location hints (ONB-2, design 03 step 2). Purpose: catch aliases (former,
// Chinese, colloquial trading names), directory listings, and practitioners the
// site omits. It reuses the monitoring pipeline's PromptRunner plumbing.
//
// A missing name is bad input and non-retryable. Provider errors the runner
// marks non-retryable (400-class, content-policy refusals) won't fix on retry,
// so they become non-retryable too; every other error propagates for Temporal's
// default retry. An empty summary is returned as-is — ONB-4's failure posture
// decides whether research-only is enough, not this thin wrapper.
func (a *Activities) ResearchBusiness(ctx context.Context, in ResearchBusinessInput) (ResearchBusinessOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return ResearchBusinessOutput{}, temporal.NewNonRetryableApplicationError(
			"research business", "BadInput", errors.New("business name is required"))
	}

	result, err := a.Runner.RunPrompt(ctx, llm.PromptRequest{
		Prompt:   buildResearchPrompt(name, in.Location),
		Location: in.Location,
	})
	if err != nil {
		if errors.Is(err, llm.ErrNonRetryable) {
			return ResearchBusinessOutput{}, temporal.NewNonRetryableApplicationError(
				"research business", "ResearchRefused", err)
		}
		return ResearchBusinessOutput{}, err
	}

	return ResearchBusinessOutput{
		Summary: strings.TrimSpace(result.ResponseText),
		Model:   result.Model,
	}, nil
}

// buildResearchPrompt composes the web_search instructions for one business. It
// is pure so the prompt contract can be unit-tested without a live call.
func buildResearchPrompt(name string, location llm.Location) string {
	var b strings.Builder
	b.WriteString("You are researching a business to help build a monitoring profile. ")
	b.WriteString("Search the web for the business below and report what you find.\n\n")
	b.WriteString("Business name: ")
	b.WriteString(name)
	b.WriteString("\n")
	if hints := locationHints(location); hints != "" {
		b.WriteString("Location: ")
		b.WriteString(hints)
		b.WriteString("\n")
	}
	b.WriteString(`
Report these, and only these:
1. Alternative names for the *organization* — former names, Chinese names, colloquial or commonly-used names, and abbreviations it trades under. These are trading identities of the business itself, not the names of individual people. Include a person's name only if it is genuinely part of the trading name (e.g. "Dr Tan's Orthopaedic Practice").
2. Online directory and profile listings (Google, health directories, professional registries, review sites).
3. Practitioners associated with the business — doctors, specialists, or staff — especially any not obviously listed on the business's own website, with their roles where known.

If you find nothing for a section, say so. Be concise and factual, and cite the sources you relied on.`)
	return b.String()
}

// locationHints renders the human-readable location parts for the prompt text.
// City and Region are natural place names; Country is a two-letter ISO code but
// still a useful disambiguator for the model.
func locationHints(location llm.Location) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{location.City, location.Region, location.Country} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}
