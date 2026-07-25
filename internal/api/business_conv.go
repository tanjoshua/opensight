package api

import (
	"encoding/json"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/llm"
	"opensight/internal/store"
	"opensight/internal/workflows"
)

// businessToProfile decodes a store.Business's raw services/location JSONB
// columns into an llm.ProposedProfile, the shared shape the RPC responses
// build on (businessProfileToProto below).
func businessToProfile(b store.Business) (llm.ProposedProfile, error) {
	profile := llm.ProposedProfile{Name: b.Name, Aliases: b.Aliases, Services: []string{}}
	if b.Category != nil {
		profile.Category = *b.Category
	}
	if len(b.Services) > 0 && string(b.Services) != "null" {
		if err := json.Unmarshal(b.Services, &profile.Services); err != nil {
			return llm.ProposedProfile{}, err
		}
	}
	if len(b.Location) > 0 && string(b.Location) != "null" {
		if err := json.Unmarshal(b.Location, &profile.Location); err != nil {
			return llm.ProposedProfile{}, err
		}
	}
	return profile, nil
}

// Nil-safety and normalization rules (load-bearing, not cosmetic):
//
//   - Every *ToProto function returns a non-nil message with non-nil required
//     nested messages (so resp.GetLocation() chains never nil-panic on the
//     client side) — this matches REST, which always emitted a location object
//     even when empty.
//   - proposalPayloadFromProto(nil) returns llm.ProposalPayload{}, NOT a panic.
//     This lets ApplyProposal with an omitted payload fail llm.ValidateProposal
//     naturally (empty name/category/country) rather than crashing.
//   - *FromProto functions normalize nil repeated fields (Aliases, Services,
//     Prompts, Sources) to non-nil empty slices. Reason: json.Marshal(nil
//     []string) produces null, but json.Marshal([]string{}) produces [], and
//     ApplyProposal marshals Services straight into a jsonb column — a stray
//     null would silently differ from what REST always wrote.
//   - *ToProto functions do NOT normalize the reverse direction — a nil llm
//     slice becomes a nil proto slice, which is wire-identical to an empty
//     one. This means the round-trip test needs a fully-populated fixture with
//     no nil slices, not an arbitrary one.

func locationToProto(l llm.ProposedLocation) *opensightv1.Location {
	return &opensightv1.Location{
		Address: l.Address,
		Area:    l.Area,
		City:    l.City,
		Country: l.Country,
	}
}

func locationFromProto(l *opensightv1.Location) llm.ProposedLocation {
	if l == nil {
		return llm.ProposedLocation{}
	}
	return llm.ProposedLocation{
		Address: l.GetAddress(),
		Area:    l.GetArea(),
		City:    l.GetCity(),
		Country: l.GetCountry(),
	}
}

func proposedProfileToProto(p llm.ProposedProfile) *opensightv1.ProposedProfile {
	return &opensightv1.ProposedProfile{
		Name:     p.Name,
		Aliases:  p.Aliases,
		Category: p.Category,
		Services: p.Services,
		Location: locationToProto(p.Location),
	}
}

func proposedProfileFromProto(p *opensightv1.ProposedProfile) llm.ProposedProfile {
	if p == nil {
		return llm.ProposedProfile{}
	}
	aliases := p.GetAliases()
	if aliases == nil {
		aliases = []string{}
	}
	services := p.GetServices()
	if services == nil {
		services = []string{}
	}
	return llm.ProposedProfile{
		Name:     p.GetName(),
		Aliases:  aliases,
		Category: p.GetCategory(),
		Services: services,
		Location: locationFromProto(p.GetLocation()),
	}
}

func proposalPayloadToProto(p llm.ProposalPayload) *opensightv1.ProposalPayload {
	prompts := make([]*opensightv1.ProposedPrompt, len(p.Prompts))
	for i, pr := range p.Prompts {
		prompts[i] = &opensightv1.ProposedPrompt{Text: pr.Text}
	}
	sources := make([]*opensightv1.ProposalSource, len(p.Sources))
	for i, s := range p.Sources {
		sources[i] = &opensightv1.ProposalSource{Url: s.URL, Title: s.Title, Domain: s.Domain}
	}
	return &opensightv1.ProposalPayload{
		LowConfidence: p.LowConfidence,
		Profile:       proposedProfileToProto(p.Profile),
		Prompts:       prompts,
		Sources:       sources,
	}
}

func proposalPayloadFromProto(p *opensightv1.ProposalPayload) llm.ProposalPayload {
	if p == nil {
		return llm.ProposalPayload{}
	}
	protoPrompts := p.GetPrompts()
	prompts := make([]llm.ProposedPrompt, len(protoPrompts))
	for i, pr := range protoPrompts {
		prompts[i] = llm.ProposedPrompt{Text: pr.GetText()}
	}
	protoSources := p.GetSources()
	sources := make([]llm.ProposalSource, len(protoSources))
	for i, s := range protoSources {
		sources[i] = llm.ProposalSource{URL: s.GetUrl(), Title: s.GetTitle(), Domain: s.GetDomain()}
	}
	return llm.ProposalPayload{
		LowConfidence: p.GetLowConfidence(),
		Profile:       proposedProfileFromProto(p.GetProfile()),
		Prompts:       prompts,
		Sources:       sources,
	}
}

func planToProto(p store.Plan) *opensightv1.Plan {
	return &opensightv1.Plan{
		Slug:        p.Slug,
		PromptLimit: int32(p.PromptLimit),
		RunInterval: p.RunInterval,
		Platforms:   p.Platforms,
	}
}

// businessProfileToProto wraps businessToProfile above to build the full
// BusinessProfile message.
func businessProfileToProto(b store.Business, plan store.Plan) (*opensightv1.BusinessProfile, error) {
	profile, err := businessToProfile(b)
	if err != nil {
		return nil, err
	}
	resp := &opensightv1.BusinessProfile{
		Id:       b.ID.String(),
		Status:   businessStatusToProto(b.Status),
		Name:     profile.Name,
		Website:  b.Website,
		Aliases:  profile.Aliases,
		Category: b.Category,
		Services: profile.Services,
		Location: locationToProto(profile.Location),
		Plan:     planToProto(plan),
	}
	return resp, nil
}

// proposalStatusToProto maps the REST proposalStatus* string constants
// (businesses.go) to the generated proto enum.
func proposalStatusToProto(status string) opensightv1.ProposalStatus {
	switch status {
	case proposalStatusGenerating:
		return opensightv1.ProposalStatus_PROPOSAL_STATUS_GENERATING
	case proposalStatusReady:
		return opensightv1.ProposalStatus_PROPOSAL_STATUS_READY
	case proposalStatusFailed:
		return opensightv1.ProposalStatus_PROPOSAL_STATUS_FAILED
	default:
		return opensightv1.ProposalStatus_PROPOSAL_STATUS_UNSPECIFIED
	}
}

// generationStageToProto maps GenerateProfileWorkflow's stage strings
// (internal/workflows) to the generated proto enum.
func generationStageToProto(stage string) opensightv1.GenerationStage {
	switch stage {
	case workflows.GenerationStageFetchingSite:
		return opensightv1.GenerationStage_GENERATION_STAGE_FETCHING_SITE
	case workflows.GenerationStageDrafting:
		return opensightv1.GenerationStage_GENERATION_STAGE_DRAFTING
	default:
		return opensightv1.GenerationStage_GENERATION_STAGE_UNSPECIFIED
	}
}

// businessStatusToProto maps the store's string-typed business status to the
// generated proto enum. Moved from auth_rpc.go (GetMe uses it too).
func businessStatusToProto(s store.BusinessStatus) opensightv1.BusinessStatus {
	switch s {
	case store.BusinessStatusDraft:
		return opensightv1.BusinessStatus_BUSINESS_STATUS_DRAFT
	case store.BusinessStatusActive:
		return opensightv1.BusinessStatus_BUSINESS_STATUS_ACTIVE
	default:
		return opensightv1.BusinessStatus_BUSINESS_STATUS_UNSPECIFIED
	}
}
