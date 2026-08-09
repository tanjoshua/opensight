package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const MaxSourceClassificationAttempts = 2

var ErrSourceClassificationValidation = errors.New("source classification validation failed")

const (
	SourceCompetitorOwned = "competitor_owned"
	SourceThirdParty      = "third_party"
	SourceUnknown         = "unknown"
)

type SourcePage struct {
	URL     string `json:"url"`
	Content string `json:"content"`
}

type SourceClaim struct {
	Owner   string `json:"owner"`
	Passage string `json:"passage"`
}

type SourceCandidate struct {
	Domain string        `json:"domain"`
	Pages  []SourcePage  `json:"pages"`
	Claims []SourceClaim `json:"claims"`
}

type SourceClassificationInput struct {
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

type SourceClassificationRunResult struct {
	RawJSON json.RawMessage
	Model   string
}

// SourceClassifier makes one validated batched ownership decision for all
// inspected citation sources in an assessment.
type SourceClassifier interface {
	ClassifySources(context.Context, SourceClassificationInput) ([]SourceClassification, error)
}

type sourceClassificationOutput struct {
	Sources []SourceClassification `json:"sources"`
}

type sourceClassificationCaller interface {
	runSourceClassification(context.Context, SourceClassificationInput) (SourceClassificationRunResult, error)
}

func classifySourcesWithRetry(ctx context.Context, caller sourceClassificationCaller, in SourceClassificationInput) ([]SourceClassification, error) {
	for attempt := 0; attempt < MaxSourceClassificationAttempts; attempt++ {
		result, err := caller.runSourceClassification(ctx, in)
		if err != nil {
			return nil, err
		}
		var out sourceClassificationOutput
		var validationErrs []string
		if err := json.Unmarshal(result.RawJSON, &out); err != nil {
			validationErrs = []string{fmt.Sprintf("output is not valid JSON matching the schema: %v", err)}
		} else {
			validationErrs = validateSourceClassifications(out.Sources, in.Candidates)
		}
		if len(validationErrs) == 0 {
			return out.Sources, nil
		}
		in.PriorOutputJSON = result.RawJSON
		in.RetryValidationErrors = validationErrs
		if attempt == MaxSourceClassificationAttempts-1 {
			return nil, fmt.Errorf("%w after %d attempts: %s", ErrSourceClassificationValidation, MaxSourceClassificationAttempts, strings.Join(validationErrs, "; "))
		}
	}
	panic("unreachable")
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

func (s *StubSourceClassifier) ClassifySources(_ context.Context, in SourceClassificationInput) ([]SourceClassification, error) {
	if s == nil {
		return nil, errors.New("stub source classifier is nil")
	}
	out := make([]SourceClassification, len(in.Candidates))
	for i := range out {
		out[i] = SourceClassification{CandidateIndex: i, Kind: SourceUnknown, ClaimIndices: []int{}}
	}
	return out, nil
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
