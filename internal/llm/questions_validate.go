package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MaxQuestionsAttempts is the in-activity validation-retry budget, the same
// "retry once with the validation errors appended" posture as
// MaxProposeProfileAttempts.
const MaxQuestionsAttempts = 2

// minNameLeakLength skips name needles shorter than this when checking
// generated questions for business-name leakage: a business literally named
// "Q" would otherwise false-positive against ordinary words that happen to
// contain that letter.
const minNameLeakLength = 3

// DecodeQuestionsOutput repairs the known double-escaped-unicode model
// artifact (RepairDoubleEscapedUnicode — the same artifact seen in extraction
// and propose-profile output) and unmarshals the result. Callers must use this
// rather than json.Unmarshal directly on a runner's RawJSON.
func DecodeQuestionsOutput(raw json.RawMessage) ([]ProposedPrompt, error) {
	var out questionsOutput
	err := json.Unmarshal(RepairDoubleEscapedUnicode(raw), &out)
	return out.Prompts, err
}

// ValidateQuestions runs every deterministic check on generated questions
// (design 03's "Prompt generation rules"): the exact
// billing.Plan.PromptLimit count, no empty text, and no business-name/alias
// leakage. It returns one human-readable message per violation; an empty
// result means valid. It is pure.
func ValidateQuestions(prompts []ProposedPrompt, in QuestionsInput) []string {
	var errs []string
	if len(prompts) != in.PromptLimit {
		errs = append(errs, fmt.Sprintf("prompts has %d entries, want exactly %d (billing.Plan.PromptLimit)", len(prompts), in.PromptLimit))
	}
	for i, pr := range prompts {
		if strings.TrimSpace(pr.Text) == "" {
			errs = append(errs, fmt.Sprintf("prompts[%d].text is empty", i))
		}
	}
	errs = append(errs, validatePromptNameLeakage(prompts, in)...)
	return errs
}

// validatePromptNameLeakage flags any question whose text contains the
// business name or an alias. Needles come from in.Name and in.Aliases;
// matching is whitespace-normalized and case-insensitive; needles shorter
// than minNameLeakLength are skipped.
func validatePromptNameLeakage(prompts []ProposedPrompt, in QuestionsInput) []string {
	seen := map[string]bool{}
	var needles []string
	for _, n := range append([]string{in.Name}, in.Aliases...) {
		norm := strings.ToLower(normalizeForVerbatimCheck(n))
		if len(norm) < minNameLeakLength || seen[norm] {
			continue
		}
		seen[norm] = true
		needles = append(needles, norm)
	}

	var errs []string
	for i, pr := range prompts {
		hay := strings.ToLower(normalizeForVerbatimCheck(pr.Text))
		for _, needle := range needles {
			if strings.Contains(hay, needle) {
				errs = append(errs, fmt.Sprintf("prompts[%d].text contains the business name/alias %q; questions must never name the business", i, needle))
			}
		}
	}
	return errs
}

// QuestionsAttemptResult is the outcome of GenerateQuestionsWithRetry.
type QuestionsAttemptResult struct {
	Prompts        []ProposedPrompt
	Model          string
	Generated      bool     // false if validation failed after MaxQuestionsAttempts
	ValidationErrs []string // the final attempt's errors, always populated when !Generated
}

// GenerateQuestionsWithRetry runs the question-generation call, decodes and
// validates the output, and retries once with the validation errors appended
// to the model if validation fails. It mirrors ProposeWithRetry: a hard runner
// error is the caller's to retry; a decode failure folds into the validation
// errors as a self-correctable message.
func GenerateQuestionsWithRetry(ctx context.Context, runner QuestionsRunner, in QuestionsInput) (QuestionsAttemptResult, error) {
	for attempt := 0; attempt < MaxQuestionsAttempts; attempt++ {
		res, err := runner.RunQuestions(ctx, in)
		if err != nil {
			return QuestionsAttemptResult{}, err
		}

		prompts, decodeErr := DecodeQuestionsOutput(res.RawJSON)
		var validationErrs []string
		if decodeErr != nil {
			validationErrs = []string{fmt.Sprintf("output is not valid JSON matching the schema: %v", decodeErr)}
		} else {
			validationErrs = ValidateQuestions(prompts, in)
		}

		if len(validationErrs) == 0 {
			return QuestionsAttemptResult{Prompts: prompts, Model: res.Model, Generated: true}, nil
		}
		in.PriorOutputJSON = res.RawJSON
		in.RetryValidationErrors = validationErrs
		if attempt == MaxQuestionsAttempts-1 {
			return QuestionsAttemptResult{Model: res.Model, Generated: false, ValidationErrs: validationErrs}, nil
		}
	}
	panic("unreachable") // MaxQuestionsAttempts is always >= 1
}
