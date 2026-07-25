package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ProposeProfileInput is one ProposeProfile request: the user-entered
// name/website plus the FetchSite text (primary evidence, "" when FetchSite
// failed) the model researches and turns into a structured profile proposal, and
// the number of monitoring prompts to generate (plan.prompt_limit — never
// hardcoded). The model does its own web_search over the name/website; there is
// no separate research step.
type ProposeProfileInput struct {
	Name        string
	Website     string
	SiteText    string
	Location    Location // web_search user_location hint (SG for the MVP market)
	PromptLimit int

	// Set only on the one allowed validation retry: the model's prior output and
	// the deterministic validation failures to correct.
	PriorOutputJSON       json.RawMessage
	RetryValidationErrors []string
}

// ProposeProfileRunResult is the raw output of one proposal call. The model's
// JSON text is left unmarshalled for ProposeWithRetry to decode and validate —
// the same runner/decoder split as extraction and match. RawResponse is the full
// response body, kept so the caller can derive Sources and detect the model's
// web-search actions (SourcesFromWebSearch / ParseWebSearchActions).
type ProposeProfileRunResult struct {
	RawJSON     json.RawMessage
	RawResponse json.RawMessage
	Model       string
}

// ProposalSource is one source link surfaced to the user in the review screen:
// a page the model opened while researching. URL is the cleaned link, Domain the
// bare host to label it by. These are computed server-side from the response's
// web-search actions, not asked of the model, so they are never part of the
// strict output schema.
type ProposalSource struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Domain string `json:"domain"`
}

// ProposedLocation is the business location. country is an ISO 3166-1 alpha-2
// code and is always required (design 03).
type ProposedLocation struct {
	Address string `json:"address"`
	Area    string `json:"area"`
	City    string `json:"city"`
	Country string `json:"country"`
}

// ProposedProfile is the structured business profile the user reviews.
type ProposedProfile struct {
	Name     string           `json:"name"`
	Aliases  []string         `json:"aliases"`
	Category string           `json:"category"`
	Services []string         `json:"services"`
	Location ProposedLocation `json:"location"`
}

// ProposedPrompt is one generated monitoring prompt.
type ProposedPrompt struct {
	Text string `json:"text"`
}

// ProposalPayload is the decoded proposal (design 03, "Proposal payload"). It is
// what the ProposeProfile activity returns and what ONB-4 marshals verbatim into
// profile_proposals.payload.
type ProposalPayload struct {
	LowConfidence bool             `json:"low_confidence"`
	Profile       ProposedProfile  `json:"profile"`
	Prompts       []ProposedPrompt `json:"prompts"`
	// Sources is server-populated from the model's web-search actions (not part of
	// the model's own JSON schema); the review screen renders it read-only.
	Sources []ProposalSource `json:"sources,omitempty"`
}

// ProposeProfileRunner runs one structured-output proposal call per onboarding
// (design 03 step 3). Mirrors ExtractionRunner's mode split.
type ProposeProfileRunner interface {
	RunProposeProfile(ctx context.Context, in ProposeProfileInput) (ProposeProfileRunResult, error)
}

// NewProposeProfileRunner selects a ProposeProfileRunner by mode, mirroring
// NewExtractionRunner. "stub" and "replay" spend no OpenAI money; "openai" is
// the real call. "replay" is aliased to the stub — building a proposal
// replay-fixture corpus is not worth it for a once-per-onboarding call.
// openAICfg is only consulted for "openai".
func NewProposeProfileRunner(mode string, openAICfg OpenAIConfig) (ProposeProfileRunner, error) {
	switch mode {
	case "stub", "replay":
		return NewStubProposeProfileRunner()
	case "openai":
		return NewOpenAIProposeProfileRunner(openAICfg)
	default:
		return nil, fmt.Errorf("unknown propose profile runner mode %q", mode)
	}
}

// StubProposeProfileRunner returns a trivially-valid proposal built from the
// input: exactly PromptLimit generic prompts (none containing the business
// name), so the dev default boots without an OpenAI key.
type StubProposeProfileRunner struct{}

// NewStubProposeProfileRunner returns the offline stub proposer.
func NewStubProposeProfileRunner() (*StubProposeProfileRunner, error) {
	return &StubProposeProfileRunner{}, nil
}

// stubProposalPrompts are varied, name-free consumer questions the stub cycles
// through to satisfy validation offline.
var stubProposalPrompts = []ProposedPrompt{
	{Text: "best orthopaedic clinic in Singapore"},
	{Text: "where can I get ACL reconstruction in Singapore"},
	{Text: "knee pain that won't go away, who should I see in Singapore"},
	{Text: "top rated orthopaedic specialist in Singapore"},
}

// RunProposeProfile returns a canned proposal shaped by the input. LowConfidence
// is true because a stub has done no real research; the model id is a fixed stub
// marker so a stubbed proposal is distinguishable from a real one.
func (r *StubProposeProfileRunner) RunProposeProfile(_ context.Context, in ProposeProfileInput) (ProposeProfileRunResult, error) {
	if r == nil {
		return ProposeProfileRunResult{}, fmt.Errorf("stub propose profile runner is nil")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Sample Clinic"
	}
	prompts := make([]ProposedPrompt, 0, in.PromptLimit)
	for i := 0; i < in.PromptLimit; i++ {
		prompts = append(prompts, stubProposalPrompts[i%len(stubProposalPrompts)])
	}
	payload := ProposalPayload{
		LowConfidence: true,
		Profile: ProposedProfile{
			Name:     name,
			Aliases:  []string{},
			Category: "orthopaedic clinic",
			Services: []string{"consultation"},
			Location: ProposedLocation{City: "Singapore", Country: "SG"},
		},
		Prompts: prompts,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ProposeProfileRunResult{}, err
	}
	return ProposeProfileRunResult{RawJSON: raw, Model: "stub-propose-profile"}, nil
}
