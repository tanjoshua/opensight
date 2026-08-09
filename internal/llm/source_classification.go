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
	Owner          string `json:"owner"`
	ClaimIndices   []int  `json:"claim_indices"`
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
	Key            string                 `json:"topic_key"`
	Title          string                 `json:"title"`
	Reason         string                 `json:"reason"`
	Recommendation string                 `json:"recommendation"`
	Coverage       string                 `json:"coverage"`
	SiteEvidence   []string               `json:"site_evidence"`
	Evidence       []SourceClaimReference `json:"evidence"`
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
			validationErrs = validateSourceAnalysis(out, in)
		}
		if len(validationErrs) == 0 {
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

func validateSourceAnalysis(out SourceAnalysis, in SourceClassificationInput) []string {
	errs := validateSourceClassifications(out.Sources, in.Candidates)
	if out.Gaps == nil {
		errs = append(errs, "content_gaps is missing; use an empty array when there are no gaps")
	}
	if strings.TrimSpace(in.SiteContent) == "" && len(out.Gaps) != 0 {
		errs = append(errs, "content_gaps must be empty when customer-site content is unavailable")
	}
	classifications := map[int]SourceClassification{}
	for _, classification := range out.Sources {
		classifications[classification.CandidateIndex] = classification
	}
	keys := map[string]bool{}
	for i, gap := range out.Gaps {
		if !ValidContentGapKey(gap.Key) {
			errs = append(errs, fmt.Sprintf("content_gaps[%d] has invalid topic_key %q", i, gap.Key))
		}
		if reservedContentGapKeys[gap.Key] {
			errs = append(errs, fmt.Sprintf("content_gaps[%d] topic_key %q is too generic", i, gap.Key))
		}
		if keys[gap.Key] {
			errs = append(errs, fmt.Sprintf("content gap topic_key %q is duplicated", gap.Key))
		}
		keys[gap.Key] = true
		if strings.TrimSpace(gap.Title) == "" || strings.TrimSpace(gap.Reason) == "" || strings.TrimSpace(gap.Recommendation) == "" {
			errs = append(errs, fmt.Sprintf("content_gaps[%d] requires title, reason, and recommendation", i))
		}
		switch gap.Coverage {
		case "absent":
			if len(gap.SiteEvidence) != 0 {
				errs = append(errs, fmt.Sprintf("content_gaps[%d] absent coverage must have empty site_evidence", i))
			}
		case "partial":
			if len(gap.SiteEvidence) == 0 {
				errs = append(errs, fmt.Sprintf("content_gaps[%d] partial coverage requires site_evidence", i))
			}
		default:
			errs = append(errs, fmt.Sprintf("content_gaps[%d] has invalid coverage %q", i, gap.Coverage))
		}
		for _, passage := range gap.SiteEvidence {
			if strings.TrimSpace(passage) == "" || !strings.Contains(in.SiteContent, passage) {
				errs = append(errs, fmt.Sprintf("content_gaps[%d] site_evidence is not verbatim customer-site content", i))
			}
		}
		if len(gap.Evidence) == 0 {
			errs = append(errs, fmt.Sprintf("content_gaps[%d] requires competitor-owned evidence", i))
		}
		seenEvidence := map[SourceClaimReference]bool{}
		for _, ref := range gap.Evidence {
			if seenEvidence[ref] {
				errs = append(errs, fmt.Sprintf("content_gaps[%d] duplicates evidence candidate %d claim %d", i, ref.CandidateIndex, ref.ClaimIndex))
			}
			seenEvidence[ref] = true
			if ref.CandidateIndex < 0 || ref.CandidateIndex >= len(in.Candidates) {
				errs = append(errs, fmt.Sprintf("content_gaps[%d] candidate index %d is out of range", i, ref.CandidateIndex))
				continue
			}
			if ref.ClaimIndex < 0 || ref.ClaimIndex >= len(in.Candidates[ref.CandidateIndex].Claims) {
				errs = append(errs, fmt.Sprintf("content_gaps[%d] claim index %d is out of range", i, ref.ClaimIndex))
				continue
			}
			classification, ok := classifications[ref.CandidateIndex]
			if !ok || classification.Kind != SourceCompetitorOwned || !containsInt(classification.ClaimIndices, ref.ClaimIndex) {
				errs = append(errs, fmt.Sprintf("content_gaps[%d] evidence candidate %d claim %d was not selected as competitor-owned", i, ref.CandidateIndex, ref.ClaimIndex))
			}
		}
		copy := strings.ToLower(gap.Title + " " + gap.Reason + " " + gap.Recommendation)
		for _, candidate := range in.Candidates {
			for _, claim := range candidate.Claims {
				owner := strings.ToLower(strings.TrimSpace(claim.Owner))
				if owner != "" && strings.Contains(copy, owner) {
					errs = append(errs, fmt.Sprintf("content_gaps[%d] user-facing copy names competitor %q", i, claim.Owner))
				}
				if ownerKey := contentKey(claim.Owner); ownerKey != "" && strings.Contains(gap.Key, ownerKey) {
					errs = append(errs, fmt.Sprintf("content_gaps[%d] topic_key names competitor %q", i, claim.Owner))
				}
			}
		}
	}
	return errs
}

func ValidContentGapKey(key string) bool {
	return len(key) >= 3 && len(key) <= 64 && contentGapKeyPattern.MatchString(key) && !reservedContentGapKeys[key]
}

func contentKey(value string) string {
	var b strings.Builder
	hyphen := true
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			hyphen = false
		} else if !hyphen {
			b.WriteByte('-')
			hyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

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
		case SourceThirdParty, SourceUnknown:
			if strings.TrimSpace(classification.Owner) != "" || len(classification.ClaimIndices) != 0 {
				errs = append(errs, fmt.Sprintf("candidate %d classification %s must have empty owner and claim_indices", index, classification.Kind))
			}
		case SourceCompetitorOwned:
			owner := strings.TrimSpace(classification.Owner)
			if owner == "" {
				errs = append(errs, fmt.Sprintf("candidate %d competitor_owned classification requires an owner", index))
			}
			if len(classification.ClaimIndices) == 0 {
				errs = append(errs, fmt.Sprintf("candidate %d competitor_owned classification requires selected claims", index))
			}
			claimSeen := map[int]bool{}
			for _, claimIndex := range classification.ClaimIndices {
				if claimIndex < 0 || claimIndex >= len(candidates[index].Claims) {
					errs = append(errs, fmt.Sprintf("candidate %d claim index %d is out of range", index, claimIndex))
					continue
				}
				if claimSeen[claimIndex] {
					errs = append(errs, fmt.Sprintf("candidate %d claim index %d is duplicated", index, claimIndex))
				}
				claimSeen[claimIndex] = true
				if candidates[index].Claims[claimIndex].Owner != owner {
					errs = append(errs, fmt.Sprintf("candidate %d claim index %d belongs to %q, not selected owner %q", index, claimIndex, candidates[index].Claims[claimIndex].Owner, owner))
				}
			}
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
