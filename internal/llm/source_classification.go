package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const MaxSourceClassificationAttempts = 2

var ErrSourceClassificationValidation = errors.New("source classification validation failed")

const (
	SourceCompetitorOwned = "competitor_owned"
	SourceThirdParty      = "third_party"
	SourceUnknown         = "unknown"
)

var contentGapKeyPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var reservedContentGapKeys = map[string]bool{
	"other": true, "content": true, "content-gap": true, "website-content": true, "business-details": true,
}

type SourcePage struct {
	URL     string `json:"url"`
	Content string `json:"content"`
}

type SourceClaim struct {
	Owner    string `json:"owner"`
	Passage  string `json:"passage"`
	ResultID string `json:"result_id"`
	PromptID string `json:"prompt_id"`
}

type SourceCandidate struct {
	Domain string        `json:"domain"`
	Pages  []SourcePage  `json:"pages"`
	Claims []SourceClaim `json:"claims"`
}

type PriorContentGap struct {
	Key            string `json:"topic_key"`
	Title          string `json:"title"`
	Recommendation string `json:"recommendation"`
}

type SourceClassificationInput struct {
	BusinessName          string
	SiteContent           string
	PriorContentGaps      []PriorContentGap
	Candidates            []SourceCandidate
	PriorOutputJSON       json.RawMessage
	RetryValidationErrors []string
}

type SourceClassification struct {
	CandidateIndex int    `json:"candidate_index"`
	Kind           string `json:"classification"`
	// Owner and ClaimIndices make the model name the competitor and cite the
	// claims before grouping them. Nothing reads them, so they are deliberately
	// never validated: a reasoning aid must not be able to fail the run.
	Owner        string `json:"owner"`
	ClaimIndices []int  `json:"claim_indices"`
}

type SourceClaimReference struct {
	CandidateIndex int `json:"candidate_index"`
	ClaimIndex     int `json:"claim_index"`
}

// ContentGap is one model-grouped publishing job that competitor-owned evidence
// repeatedly made useful in answers, but the customer's readable site does not
// cover clearly enough. Key is constrained to a durable semantic slug so the
// model controls the grouping without making finding identity presentation copy.
type ContentGap struct {
	Key            string `json:"topic_key"`
	Title          string `json:"title"`
	Reason         string `json:"reason"`
	Recommendation string `json:"recommendation"`
	// Coverage and SiteEvidence make the model check the customer's site before
	// claiming a gap. Neither is validated: SiteEvidence is read by nobody, and
	// an unexpected Coverage value only costs one sentence of finding detail.
	Coverage     string                 `json:"coverage"`
	SiteEvidence []string               `json:"site_evidence"`
	Evidence     []SourceClaimReference `json:"evidence"`
}

type SourceAnalysis struct {
	Sources []SourceClassification `json:"sources"`
	Gaps    []ContentGap           `json:"content_gaps"`
}

type SourceClassificationRunResult struct {
	RawJSON json.RawMessage
	Model   string
}

// SourceClassifier makes one validated batched ownership decision for all
// inspected citation sources and groups customer-site gaps across the sources
// that are competitor-owned.
type SourceClassifier interface {
	ClassifySources(context.Context, SourceClassificationInput) (SourceAnalysis, error)
}

type sourceClassificationCaller interface {
	runSourceClassification(context.Context, SourceClassificationInput) (SourceClassificationRunResult, error)
}

func classifySourcesWithRetry(ctx context.Context, caller sourceClassificationCaller, in SourceClassificationInput) (SourceAnalysis, error) {
	for attempt := 0; attempt < MaxSourceClassificationAttempts; attempt++ {
		result, err := caller.runSourceClassification(ctx, in)
		if err != nil {
			return SourceAnalysis{}, err
		}
		var out SourceAnalysis
		var validationErrs []string
		if err := json.Unmarshal(result.RawJSON, &out); err != nil {
			validationErrs = []string{fmt.Sprintf("output is not valid JSON matching the schema: %v", err)}
		} else {
			validationErrs = validateSourceClassifications(out.Sources, in.Candidates)
		}
		if len(validationErrs) == 0 {
			out.Gaps = usableContentGaps(out.Gaps, out.Sources, in)
			return out, nil
		}
		in.PriorOutputJSON = result.RawJSON
		in.RetryValidationErrors = validationErrs
		if attempt == MaxSourceClassificationAttempts-1 {
			return SourceAnalysis{}, fmt.Errorf("%w after %d attempts: %s", ErrSourceClassificationValidation, MaxSourceClassificationAttempts, strings.Join(validationErrs, "; "))
		}
	}
	panic("unreachable")
}

// usableContentGaps keeps the gaps the product can render and silently drops
// the rest. A gap the model got wrong costs that one publishing job, never the
// whole assessment, so nothing here retries or fails.
func usableContentGaps(gaps []ContentGap, sources []SourceClassification, in SourceClassificationInput) []ContentGap {
	out := []ContentGap{}
	// Without readable customer-site content, absence cannot be verified.
	if strings.TrimSpace(in.SiteContent) == "" {
		return out
	}
	// Classifications are validated before this runs, so every index here is in
	// range for in.Candidates.
	competitorOwned := map[int]bool{}
	for _, classification := range sources {
		if classification.Kind == SourceCompetitorOwned {
			competitorOwned[classification.CandidateIndex] = true
		}
	}
	keys := map[string]bool{}
	for _, gap := range gaps {
		if !ValidContentGapKey(gap.Key) || keys[gap.Key] {
			continue
		}
		if strings.TrimSpace(gap.Title) == "" || strings.TrimSpace(gap.Reason) == "" || strings.TrimSpace(gap.Recommendation) == "" {
			continue
		}
		if len(gap.Evidence) == 0 || !usableEvidence(gap.Evidence, competitorOwned, in.Candidates) {
			continue
		}
		keys[gap.Key] = true
		out = append(out, gap)
	}
	return out
}

// usableEvidence reports whether every reference points at a claim of a
// competitor-owned candidate, which is what findings dereference directly.
func usableEvidence(refs []SourceClaimReference, competitorOwned map[int]bool, candidates []SourceCandidate) bool {
	for _, ref := range refs {
		if !competitorOwned[ref.CandidateIndex] {
			return false
		}
		if ref.ClaimIndex < 0 || ref.ClaimIndex >= len(candidates[ref.CandidateIndex].Claims) {
			return false
		}
	}
	return true
}

func ValidContentGapKey(key string) bool {
	return len(key) >= 3 && len(key) <= 64 && contentGapKeyPattern.MatchString(key) && !reservedContentGapKeys[key]
}

// validateSourceClassifications checks the only classification facts the
// product reads: one in-range verdict per candidate, from the known set.
// Listings findings depend on all of them, so a failure here is fatal.
func validateSourceClassifications(out []SourceClassification, candidates []SourceCandidate) []string {
	var errs []string
	if len(out) != len(candidates) {
		errs = append(errs, fmt.Sprintf("sources has %d candidates; input has %d", len(out), len(candidates)))
	}
	seen := map[int]bool{}
	for i, classification := range out {
		index := classification.CandidateIndex
		if index < 0 || index >= len(candidates) {
			errs = append(errs, fmt.Sprintf("sources[%d].candidate_index %d is out of range", i, index))
			continue
		}
		if seen[index] {
			errs = append(errs, fmt.Sprintf("candidate_index %d is duplicated", index))
		}
		seen[index] = true
		switch classification.Kind {
		case SourceCompetitorOwned, SourceThirdParty, SourceUnknown:
		default:
			errs = append(errs, fmt.Sprintf("candidate %d has invalid classification %q", index, classification.Kind))
		}
	}
	for i := range candidates {
		if !seen[i] {
			errs = append(errs, fmt.Sprintf("candidate_index %d is missing", i))
		}
	}
	return errs
}

type StubSourceClassifier struct{}

func NewStubSourceClassifier() *StubSourceClassifier { return &StubSourceClassifier{} }

func (s *StubSourceClassifier) ClassifySources(_ context.Context, in SourceClassificationInput) (SourceAnalysis, error) {
	if s == nil {
		return SourceAnalysis{}, errors.New("stub source classifier is nil")
	}
	out := make([]SourceClassification, len(in.Candidates))
	for i := range out {
		out[i] = SourceClassification{CandidateIndex: i, Kind: SourceUnknown, ClaimIndices: []int{}}
	}
	return SourceAnalysis{Sources: out, Gaps: []ContentGap{}}, nil
}

func NewSourceClassifier(mode string, cfg OpenAIConfig) (SourceClassifier, error) {
	switch mode {
	case "stub", "replay":
		return NewStubSourceClassifier(), nil
	case "openai":
		return NewOpenAISourceClassifier(cfg)
	default:
		return nil, fmt.Errorf("unknown source classifier mode %q", mode)
	}
}
