package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// MaxExtractionAttempts is the operation's validation-retry budget (design 05:
// "one retry"), independent of the River analysis job retry budget.
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
// an empty result means valid. It is pure and has no store dependency.
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
	errs = append(errs, validateCitationOccurrences(out, responseText, annotations)...)

	return errs
}

func validateCitationOccurrences(extraction ExtractionOutput, responseText string, annotations []CitationAnnotation) []string {
	out := extraction.Citations
	ordered := OrderCitationAnnotations(annotations)
	spans := AttributeCitations(responseText, annotations)
	runes := []rune(responseText)
	var errs []string
	if len(out) != len(ordered) {
		errs = append(errs, fmt.Sprintf("citations has %d occurrences; response annotations have %d", len(out), len(ordered)))
	}
	for i, c := range out {
		if c.CiteOrder != i {
			errs = append(errs, fmt.Sprintf("citations[%d].cite_order is %d; want %d", i, c.CiteOrder, i))
		}
		if i < len(ordered) && c.URL != ordered[i].URL {
			errs = append(errs, fmt.Sprintf("citations[%d].url %q does not match annotation occurrence %d url %q", i, c.URL, i, ordered[i].URL))
		}
		seen := make(map[int]bool, len(c.Links))
		for j, link := range c.Links {
			index := link.EntityIndex
			if index < 0 || index >= len(extraction.Entities) {
				errs = append(errs, fmt.Sprintf("citations[%d].links[%d].entity_index is %d; must reference an entity index in [0,%d)", i, j, index, len(extraction.Entities)))
			} else if seen[index] {
				errs = append(errs, fmt.Sprintf("citations[%d].links contains duplicate index %d", i, index))
			} else if !referenceMatchesEntity(link.Reference, extraction.Entities[index].VerbatimName) {
				errs = append(errs, fmt.Sprintf("citations[%d].links[%d].reference %q does not match entity[%d] %q", i, j, link.Reference, index, extraction.Entities[index].VerbatimName))
			}
			seen[index] = true
			if !isVerbatimSubstring(link.Reference, normalizeForVerbatimCheck(responseText)) {
				errs = append(errs, fmt.Sprintf("citations[%d].links[%d].reference %q does not appear verbatim in the response text", i, j, link.Reference))
			}
			if !isVerbatimSubstring(link.Passage, normalizeForVerbatimCheck(responseText)) {
				errs = append(errs, fmt.Sprintf("citations[%d].links[%d].passage %q does not appear verbatim in the response text", i, j, link.Passage))
			}
			if i < len(spans) {
				span := spans[i]
				passage := ""
				if span.Start >= 0 && span.Start <= span.End && span.End <= len(runes) {
					passage = string(runes[span.Start:span.End])
				}
				if !isVerbatimSubstring(link.Passage, normalizeForVerbatimCheck(passage)) {
					errs = append(errs, fmt.Sprintf("citations[%d].links[%d].passage is outside citation occurrence %d evidence span", i, j, i))
				}
			}
		}
	}
	return errs
}

// referenceMatchesEntity accepts the exact extracted name, a multi-token
// shorthand contained within it, or its acronym. It intentionally rejects a
// single generic word so a nearby "City" cannot validate a shifted business.
func referenceMatchesEntity(reference, entity string) bool {
	ref := strings.Fields(NormalizeEntityName(reference))
	name := strings.Fields(NormalizeEntityName(entity))
	if len(ref) == 0 || len(name) == 0 {
		return false
	}
	if strings.Join(ref, " ") == strings.Join(name, " ") {
		return true
	}
	if len(ref) >= 2 {
		for i := 0; i+len(ref) <= len(name); i++ {
			if strings.Join(name[i:i+len(ref)], " ") == strings.Join(ref, " ") {
				return true
			}
		}
	}
	if len(ref) == 1 {
		for _, token := range name {
			if token == ref[0] && reference == strings.ToUpper(reference) && len([]rune(reference)) >= 2 {
				return true
			}
		}
		var acronym strings.Builder
		for _, token := range name {
			if token != "and" && token != "the" {
				acronym.WriteByte(token[0])
			}
		}
		return len(ref[0]) >= 2 && ref[0] == acronym.String()
	}
	return false
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
// the single shared path used by both the AnalyzeResult operation and the
// quality-gate test driver, so a fix or regression in one is visible in both.
func ExtractWithRetry(ctx context.Context, runner ExtractionRunner, in ExtractionInput, responseText string, annotations []CitationAnnotation) (ExtractionAttemptResult, error) {
	for attempt := 0; attempt < MaxExtractionAttempts; attempt++ {
		res, err := runner.RunExtraction(ctx, in)
		if err != nil {
			// A hard extractor error (transport, provider failure) is the
			// caller's to retry (e.g. River), not this loop's.
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
