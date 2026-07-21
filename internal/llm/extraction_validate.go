package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// MaxExtractionAttempts is the in-activity validation-retry budget (design 05:
// "one retry"), separate from and orthogonal to Temporal's own
// ActivityOptions.RetryPolicy (sized by the workflow that wires this in).
const MaxExtractionAttempts = 2

var (
	sentimentEnum       = map[string]bool{"positive": true, "neutral": true, "negative": true, "mixed": true}
	citationSubjectEnum = map[string]bool{"business": true, "competitor": true, "other": true, "unknown": true}
)

// doubleEscapedUnicodeRe matches a known model artifact: instead of a single
// JSON \uXXXX escape (which json.Unmarshal decodes correctly on its own to the
// real character), some models double-escape the backslash itself (\\uXXXX in
// the raw JSON text). That decodes via one Unmarshal to a literal backslash
// followed by plain text "uXXXX" rather than the intended character — most
// visibly breaking ampersands in real business/competitor names ("Q & M
// Dental Surgery"). Collapsing \\uXXXX -> \uXXXX before unmarshalling lets the
// standard decoder resolve it to the real character, restoring a genuinely
// verbatim string.
var doubleEscapedUnicodeRe = regexp.MustCompile(`\\\\u([0-9A-Fa-f]{4})`)

// RepairDoubleEscapedUnicode collapses the double-escaped-unicode model artifact
// (see doubleEscapedUnicodeRe) so the standard JSON decoder resolves the
// intended character. Callers should prefer DecodeExtractionOutput, which
// applies this before unmarshalling.
func RepairDoubleEscapedUnicode(raw json.RawMessage) json.RawMessage {
	return doubleEscapedUnicodeRe.ReplaceAll(raw, []byte(`\u$1`))
}

// DecodeExtractionOutput repairs the known double-escaped-unicode model
// artifact (RepairDoubleEscapedUnicode) and unmarshals the result. This is the
// only correct way to turn a model's raw extraction JSON into ExtractionOutput
// — callers must never call json.Unmarshal on RunExtraction's RawJSON directly.
func DecodeExtractionOutput(raw json.RawMessage) (ExtractionOutput, error) {
	var out ExtractionOutput
	err := json.Unmarshal(RepairDoubleEscapedUnicode(raw), &out)
	return out, err
}

// normalizeForVerbatimCheck collapses runs of Unicode whitespace to a single
// ASCII space and trims the ends. Comparison after normalization is exact and
// case-sensitive — "verbatim" means verbatim; only incidental whitespace
// reformatting is forgiven.
func normalizeForVerbatimCheck(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

// isVerbatimSubstring reports whether needle, after whitespace normalization,
// is a substring of normalizedHaystack. An empty needle is never verbatim.
func isVerbatimSubstring(needle, normalizedHaystack string) bool {
	n := normalizeForVerbatimCheck(needle)
	if n == "" {
		return false
	}
	return strings.Contains(normalizedHaystack, n)
}

// ValidateExtraction runs every deterministic check on a raw extraction output
// before it is trusted enough to write: every verbatim_name and excerpt (entity
// and target) must be a verbatim, whitespace-normalized substring of
// response_text; sentiment and citation subject enum values must be allowed
// (defense in depth against schema-enforcement drift); and the output citation
// URL set must exactly match the response's annotation URL set (fabricated or
// omitted URLs are errors). It returns one human-readable message per violation;
// an empty result means valid. It is pure — no store or activity dependency.
func ValidateExtraction(out ExtractionOutput, responseText string, annotations []CitationAnnotation) []string {
	normalized := normalizeForVerbatimCheck(responseText)
	var errs []string

	for i, e := range out.Entities {
		if !isVerbatimSubstring(e.VerbatimName, normalized) {
			errs = append(errs, fmt.Sprintf("entity[%d].verbatim_name %q does not appear verbatim in the response text", i, e.VerbatimName))
		}
		if !isVerbatimSubstring(e.Excerpt, normalized) {
			errs = append(errs, fmt.Sprintf("entity[%d].excerpt %q does not appear verbatim in the response text", i, e.Excerpt))
		}
	}

	if out.Target != nil {
		if !sentimentEnum[out.Target.Sentiment] {
			errs = append(errs, fmt.Sprintf("target.sentiment %q is not one of positive, neutral, negative, mixed", out.Target.Sentiment))
		}
		for i, ex := range out.Target.Excerpts {
			if !isVerbatimSubstring(ex, normalized) {
				errs = append(errs, fmt.Sprintf("target.excerpts[%d] %q does not appear verbatim in the response text", i, ex))
			}
		}
	}

	for i, c := range out.Citations {
		if !citationSubjectEnum[c.Subject] {
			errs = append(errs, fmt.Sprintf("citations[%d].subject %q is not one of business, competitor, other, unknown", i, c.Subject))
		}
	}
	errs = append(errs, validateCitationURLSet(out.Citations, annotations)...)

	return errs
}

func validateCitationURLSet(outCitations []ExtractedCitation, annotations []CitationAnnotation) []string {
	annURLs := make(map[string]bool, len(annotations))
	for _, a := range annotations {
		annURLs[a.URL] = true
	}
	outURLs := make(map[string]bool, len(outCitations))
	for _, c := range outCitations {
		outURLs[c.URL] = true
	}

	var fabricated, omitted []string
	for u := range outURLs {
		if !annURLs[u] {
			fabricated = append(fabricated, u)
		}
	}
	for u := range annURLs {
		if !outURLs[u] {
			omitted = append(omitted, u)
		}
	}
	sort.Strings(fabricated)
	sort.Strings(omitted)

	var errs []string
	for _, u := range fabricated {
		errs = append(errs, fmt.Sprintf("citation url %q was not among the response's citation annotations (fabricated)", u))
	}
	for _, u := range omitted {
		errs = append(errs, fmt.Sprintf("citation url %q from the response annotations is missing from the output", u))
	}
	return errs
}

// ExtractionAttemptResult is the outcome of ExtractWithRetry.
type ExtractionAttemptResult struct {
	Output         ExtractionOutput
	Model          string
	Analyzed       bool     // false if validation failed after MaxExtractionAttempts
	ValidationErrs []string // the final attempt's errors, always populated when !Analyzed
}

// ExtractWithRetry runs the extraction call, decodes and validates the output,
// and retries once with the validation errors appended to the model (design
// 05's "one retry with validation errors appended") if validation fails. It is
// the single shared path used by both the AnalyzeResult activity and the
// quality-gate test driver, so a fix or regression in one is visible in both.
func ExtractWithRetry(ctx context.Context, runner ExtractionRunner, in ExtractionInput, responseText string, annotations []CitationAnnotation) (ExtractionAttemptResult, error) {
	for attempt := 0; attempt < MaxExtractionAttempts; attempt++ {
		res, err := runner.RunExtraction(ctx, in)
		if err != nil {
			// A hard extractor error (transport, provider failure) is the
			// caller's to retry (e.g. Temporal), not this loop's.
			return ExtractionAttemptResult{}, err
		}

		parsed, decodeErr := DecodeExtractionOutput(res.RawJSON)
		var validationErrs []string
		if decodeErr != nil {
			// A JSON-unmarshal failure is self-correctable via retry, not a hard
			// error: fold it into the validation error list.
			validationErrs = []string{fmt.Sprintf("output is not valid JSON matching the schema: %v", decodeErr)}
		} else {
			validationErrs = ValidateExtraction(parsed, responseText, annotations)
		}

		if len(validationErrs) == 0 {
			return ExtractionAttemptResult{Output: parsed, Model: res.Model, Analyzed: true}, nil
		}
		in.PriorOutputJSON = res.RawJSON
		in.RetryValidationErrors = validationErrs
		if attempt == MaxExtractionAttempts-1 {
			return ExtractionAttemptResult{Model: res.Model, Analyzed: false, ValidationErrs: validationErrs}, nil
		}
	}
	panic("unreachable") // MaxExtractionAttempts is always >= 1
}
