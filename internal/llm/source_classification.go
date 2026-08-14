package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const MaxSourceClassificationAttempts = 2

var ErrSourceClassificationValidation = errors.New("source classification validation failed")

const (
	SourceCompetitorOwned = "competitor_owned"
	SourceThirdParty      = "third_party"
	SourceUnknown         = "unknown"
)

var contentOpportunityKeyPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var reservedContentOpportunityKeys = map[string]bool{
	"other": true, "content": true, "content-gap": true, "website-content": true, "business-details": true,
}

type SourcePage struct {
	URL     string `json:"url"`
	Content string `json:"content"`
}

type SourceClaim struct {
	Owner    string `json:"owner"`
	Passage  string `json:"passage"`
	Question string `json:"question"`
	ResultID string `json:"result_id"`
	PromptID string `json:"prompt_id"`
}

type SourceCandidate struct {
	Domain string        `json:"domain"`
	Pages  []SourcePage  `json:"pages"`
	Claims []SourceClaim `json:"claims"`
}

type PriorContentOpportunity struct {
	Key             string `json:"topic_key"`
	Title           string `json:"title"`
	SuggestedAction string `json:"suggested_action"`
}

type SourceClassificationInput struct {
	BusinessName              string
	SiteContent               string
	PriorContentOpportunities []PriorContentOpportunity
	Candidates                []SourceCandidate
	PriorOutputJSON           json.RawMessage
	RetryValidationErrors     []string
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
	CandidateIndex int    `json:"candidate_index"`
	ClaimIndex     int    `json:"claim_index"`
	SupportedPoint string `json:"supported_point"`
}

// ContentOpportunity is one model-grouped publishing hypothesis supported by
// monitored-answer evidence and a comparison with the bounded customer-site
// crawl. The three prose fields keep observation, site state, and proposed work
// separate so presentation cannot silently turn correlation into causation.
type ContentOpportunity struct {
	Key             string                 `json:"topic_key"`
	Title           string                 `json:"title"`
	Observation     string                 `json:"observation"`
	SiteState       string                 `json:"site_state"`
	SuggestedAction string                 `json:"suggested_action"`
	Coverage        string                 `json:"coverage"`
	SiteEvidence    []string               `json:"site_evidence"`
	Evidence        []SourceClaimReference `json:"evidence"`
}

type SourceAnalysis struct {
	Sources       []SourceClassification `json:"sources"`
	Opportunities []ContentOpportunity   `json:"content_opportunities"`
}

type SourceClassificationRunResult struct {
	RawJSON json.RawMessage
	Model   string
}

// SourceClassifier makes one validated batched ownership decision for all
// inspected citation sources and groups evidence-backed content opportunities
// across the sources that are competitor-owned.
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
			out.Opportunities = usableContentOpportunities(out.Opportunities, out.Sources, in)
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

// usableContentOpportunities keeps the opportunities the product can render and
// silently drops the rest. One bad opportunity never costs the whole
// assessment, so nothing here retries or fails.
func usableContentOpportunities(opportunities []ContentOpportunity, sources []SourceClassification, in SourceClassificationInput) []ContentOpportunity {
	out := []ContentOpportunity{}
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
	for _, opportunity := range opportunities {
		if !ValidContentOpportunityKey(opportunity.Key) || keys[opportunity.Key] {
			continue
		}
		if strings.TrimSpace(opportunity.Title) == "" || strings.TrimSpace(opportunity.Observation) == "" ||
			strings.TrimSpace(opportunity.SiteState) == "" || strings.TrimSpace(opportunity.SuggestedAction) == "" {
			continue
		}
		switch opportunity.Coverage {
		case "absent":
			if len(opportunity.SiteEvidence) != 0 {
				continue
			}
		case "partial":
			if len(opportunity.SiteEvidence) == 0 || !siteEvidenceAppears(opportunity.SiteEvidence, in.SiteContent) {
				continue
			}
		default:
			continue
		}
		if len(opportunity.Evidence) == 0 || !usableEvidence(opportunity.Evidence, competitorOwned, in.Candidates) {
			continue
		}
		keys[opportunity.Key] = true
		out = append(out, opportunity)
	}
	return out
}

// usableEvidence reports whether every reference points at a claim of a
// competitor-owned candidate, which is what findings dereference directly.
func usableEvidence(refs []SourceClaimReference, competitorOwned map[int]bool, candidates []SourceCandidate) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref.SupportedPoint) == "" {
			return false
		}
		if !competitorOwned[ref.CandidateIndex] {
			return false
		}
		if ref.ClaimIndex < 0 || ref.ClaimIndex >= len(candidates[ref.CandidateIndex].Claims) {
			return false
		}
	}
	return true
}

func siteEvidenceAppears(passages []string, siteContent string) bool {
	haystack := normalizeEvidence(siteContent)
	if haystack == "" {
		return false
	}
	for _, passage := range passages {
		normalized := normalizeEvidence(passage)
		if normalized == "" || !strings.Contains(haystack, normalized) {
			return false
		}
	}
	return true
}

func normalizeEvidence(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func ValidContentOpportunityKey(key string) bool {
	return len(key) >= 3 && len(key) <= 64 && contentOpportunityKeyPattern.MatchString(key) && !reservedContentOpportunityKeys[key]
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
	return SourceAnalysis{Sources: out, Opportunities: []ContentOpportunity{}}, nil
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
