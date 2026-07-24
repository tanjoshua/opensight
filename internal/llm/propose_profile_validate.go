package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MaxProposeProfileAttempts is the in-activity validation-retry budget (design
// 03 step 3: "retry once with the validation errors appended"), separate from
// and orthogonal to Temporal's own ActivityOptions.RetryPolicy.
const MaxProposeProfileAttempts = 2

// minNameLeakLength skips name needles shorter than this when checking prompts
// for business-name leakage: a business literally named "Q" would otherwise
// false-positive against ordinary words that happen to contain that letter.
const minNameLeakLength = 3

// DecodeProposalPayload repairs the known double-escaped-unicode model artifact
// (RepairDoubleEscapedUnicode — the same artifact can occur here) and unmarshals
// the result. Unknown fields are intentionally ignored so proposals persisted
// before prompt metadata was removed remain readable. Callers must use this
// rather than json.Unmarshal directly on a runner's RawJSON.
func DecodeProposalPayload(raw json.RawMessage) (ProposalPayload, error) {
	var out ProposalPayload
	err := json.Unmarshal(RepairDoubleEscapedUnicode(raw), &out)
	return out, err
}

// ValidateProposal runs every deterministic check on a decoded proposal before
// a user sees it (design 03 step 3 and the "Prompt generation rules"): required
// profile fields, a two-letter ISO country, no empty array entries, the exact
// plan.prompt_limit prompt count, and no business-name leakage into any prompt.
// It returns one human-readable message per violation; an empty result means
// valid. It is pure.
func ValidateProposal(payload ProposalPayload, in ProposeProfileInput) []string {
	errs := ValidateProfile(payload.Profile)
	if len(payload.Prompts) != in.PromptLimit {
		errs = append(errs, fmt.Sprintf("prompts has %d entries, want exactly %d (plan.prompt_limit)", len(payload.Prompts), in.PromptLimit))
	}
	for i, pr := range payload.Prompts {
		if strings.TrimSpace(pr.Text) == "" {
			errs = append(errs, fmt.Sprintf("prompts[%d].text is empty", i))
		}
	}

	errs = append(errs, validatePromptNameLeakage(payload, in)...)

	return errs
}

// ValidateProfile applies the profile invariants shared by onboarding and
// post-activation Setup edits.
func ValidateProfile(p ProposedProfile) []string {
	var errs []string
	if strings.TrimSpace(p.Name) == "" {
		errs = append(errs, "profile.name is empty")
	}
	if strings.TrimSpace(p.Category) == "" {
		errs = append(errs, "profile.category is empty")
	}
	country := strings.TrimSpace(p.Location.Country)
	if country == "" {
		errs = append(errs, "profile.location.country is empty (a two-letter ISO country code is always required)")
	} else if !isTwoLetterCountry(country) {
		errs = append(errs, fmt.Sprintf("profile.location.country %q must be a two-letter ISO code", country))
	}
	for i, a := range p.Aliases {
		if strings.TrimSpace(a) == "" {
			errs = append(errs, fmt.Sprintf("profile.aliases[%d] is empty", i))
		}
	}
	for i, s := range p.Services {
		if strings.TrimSpace(s) == "" {
			errs = append(errs, fmt.Sprintf("profile.services[%d] is empty", i))
		}
	}
	return errs
}

// validatePromptNameLeakage flags any prompt whose text contains the business
// name or an alias. Needles come from both the user-entered in.Name and the
// model's own profile.name/aliases (the model may paraphrase the name
// differently than the user typed it). Matching is whitespace-normalized and
// case-insensitive; needles shorter than minNameLeakLength are skipped.
func validatePromptNameLeakage(payload ProposalPayload, in ProposeProfileInput) []string {
	seen := map[string]bool{}
	var needles []string
	for _, n := range append([]string{in.Name, payload.Profile.Name}, payload.Profile.Aliases...) {
		norm := strings.ToLower(normalizeForVerbatimCheck(n))
		if len(norm) < minNameLeakLength || seen[norm] {
			continue
		}
		seen[norm] = true
		needles = append(needles, norm)
	}

	var errs []string
	for i, pr := range payload.Prompts {
		hay := strings.ToLower(normalizeForVerbatimCheck(pr.Text))
		for _, needle := range needles {
			if strings.Contains(hay, needle) {
				errs = append(errs, fmt.Sprintf("prompts[%d].text contains the business name/alias %q; prompts must never name the business", i, needle))
			}
		}
	}
	return errs
}

// ProposeProfileAttemptResult is the outcome of ProposeWithRetry.
type ProposeProfileAttemptResult struct {
	Payload        ProposalPayload
	Model          string
	RawResponse    json.RawMessage // full body of the successful attempt (for Sources / web-search actions)
	Proposed       bool            // false if validation failed after MaxProposeProfileAttempts
	ValidationErrs []string        // the final attempt's errors, always populated when !Proposed
}

// ProposeWithRetry runs the proposal call, decodes and validates the output, and
// retries once with the validation errors appended to the model (design 03's
// "retry once with the validation errors appended") if validation fails. It
// mirrors ExtractWithRetry: a hard runner error is the caller's/Temporal's to
// retry; a decode failure folds into the validation errors as a
// self-correctable message.
func ProposeWithRetry(ctx context.Context, runner ProposeProfileRunner, in ProposeProfileInput) (ProposeProfileAttemptResult, error) {
	for attempt := 0; attempt < MaxProposeProfileAttempts; attempt++ {
		res, err := runner.RunProposeProfile(ctx, in)
		if err != nil {
			return ProposeProfileAttemptResult{}, err
		}

		parsed, decodeErr := DecodeProposalPayload(res.RawJSON)
		var validationErrs []string
		if decodeErr != nil {
			validationErrs = []string{fmt.Sprintf("output is not valid JSON matching the schema: %v", decodeErr)}
		} else {
			validationErrs = ValidateProposal(parsed, in)
		}

		if len(validationErrs) == 0 {
			return ProposeProfileAttemptResult{Payload: parsed, Model: res.Model, RawResponse: res.RawResponse, Proposed: true}, nil
		}
		in.PriorOutputJSON = res.RawJSON
		in.RetryValidationErrors = validationErrs
		if attempt == MaxProposeProfileAttempts-1 {
			return ProposeProfileAttemptResult{Model: res.Model, Proposed: false, ValidationErrs: validationErrs}, nil
		}
	}
	panic("unreachable") // MaxProposeProfileAttempts is always >= 1
}
