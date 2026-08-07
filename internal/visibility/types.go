// Package visibility owns the compiled visibility-practice catalog and the
// contracts used by evidence collectors, assessors, presenters and evaluators.
package visibility

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"opensight/internal/domain"
)

type AssessmentStatus string

const (
	StatusMet           AssessmentStatus = "MET"
	StatusPartial       AssessmentStatus = "PARTIAL"
	StatusNotMet        AssessmentStatus = "NOT_MET"
	StatusUnknown       AssessmentStatus = "UNKNOWN"
	StatusNotApplicable AssessmentStatus = "NOT_APPLICABLE"
)

type EvidenceTier string

const (
	TierOfficialPrerequisite EvidenceTier = "OFFICIAL_PREREQUISITE"
	TierObservedPattern      EvidenceTier = "OBSERVED_PATTERN"
	TierExperimental         EvidenceTier = "EXPERIMENTAL"
)

// PracticeDefinition is the complete description of a visibility practice.
// Everything the ranker and the presenter need about a practice lives here, so
// adding a practice is one catalog entry plus one assessor.
type PracticeDefinition struct {
	Key, Section, Title, Description string
	CriteriaVersion                  int
	Tier                             EvidenceTier
	Why                              string
	References                       []string
	// DirectBlocker marks a prerequisite whose NOT_MET result prevents the
	// other practices from working at all. The ranker orders those first.
	DirectBlocker bool
	// SubjectInTitle appends the assessed subject to the presented title. It is
	// set for practices assessed once per subject (a source domain, a tracked
	// topic) and left off for practices assessed once for the business.
	SubjectInTitle bool
	// Steps is the suggested-steps block shown on every opportunity compiled
	// from this practice.
	Steps       []string
	AssessorKey string
}

const (
	PracticeSearchAccess  = "discoverability.openai_search_access"
	PracticeAuthority     = "authority.influential_source_presence"
	PracticeTopicCoverage = "owned_site.tracked_topic_coverage"
)

var catalog = []PracticeDefinition{
	{
		Key: PracticeSearchAccess, CriteriaVersion: 1, Section: "Discoverability",
		Title:       "Allow OpenAI search access",
		Description: "Relevant public pages can be reached and indexed by OpenAI search.",
		Tier:        TierOfficialPrerequisite,
		Why:         "Blocked pages cannot be retrieved as search evidence.",
		References:  []string{"https://platform.openai.com/docs/bots"},
		AssessorKey: "search-access", DirectBlocker: true,
		Steps: []string{
			"Remove the confirmed access or indexing barrier.",
			"Publish the affected pages without authentication.",
			"Wait for a later monitoring run to verify access again.",
		},
	},
	{
		Key: PracticeAuthority, CriteriaVersion: 1, Section: "Third-party authority",
		Title:       "Be present on influential sources",
		Description: "The business is represented on sources that repeatedly shape monitored answers.",
		Tier:        TierObservedPattern,
		Why:         "Frequently cited relevant sources can shape which businesses are recommended.",
		AssessorKey: "influential-source", SubjectInTitle: true,
		Steps: []string{
			"Review the checked source and its contribution rules.",
			"Add a complete, accurate business presence without incentives or fabricated reviews.",
			"Keep claims factual and avoid competitor comparisons.",
		},
	},
	{
		Key: PracticeTopicCoverage, CriteriaVersion: 1, Section: "Owned content",
		Title:       "Cover tracked customer needs",
		Description: "Accessible owned pages clearly answer important tracked customer topics.",
		Tier:        TierObservedPattern,
		Why:         "Explicit, accessible information gives retrieval systems evidence about fit.",
		AssessorKey: "tracked-topic", SubjectInTitle: true,
		Steps: []string{
			"Answer the tracked customer need explicitly on an appropriate owned page.",
			"Include concrete service, location, eligibility, and next-step details that are true.",
			"Make the page reachable from normal site navigation.",
		},
	},
}

func Catalog() []PracticeDefinition { return append([]PracticeDefinition(nil), catalog...) }
func Practice(key string) (PracticeDefinition, bool) {
	for _, p := range catalog {
		if p.Key == key {
			return p, true
		}
	}
	return PracticeDefinition{}, false
}

type CollectorManifest struct {
	Key     string
	Version int
}
type CollectionInput struct{ AccountID, BusinessID, RunID domain.ID }
type EvidenceArtifact struct {
	CollectorKey                     string
	CollectorVersion, PayloadVersion int
	CheckedAt                        time.Time
	Payload                          json.RawMessage
}
type EvidenceCollector interface {
	Manifest() CollectorManifest
	Collect(context.Context, CollectionInput) (EvidenceArtifact, error)
}

type AssessorManifest struct {
	Key                                                       string
	ModuleVersion                                             int
	PracticeKeys, RequiredCollectors                          []string
	MaxLLMCalls, MaxWebSearches, MaxURLInspections, MaxOutput int
	MaxRuntime                                                time.Duration
}

type ResearchRequest struct{ URL string }
type ResearchResult struct {
	URL, Text string
	CheckedAt time.Time
}
type BoundedResearcher interface {
	Inspect(context.Context, ResearchRequest) (ResearchResult, error)
}
type EvidenceView interface {
	Artifact(string) (EvidenceArtifact, bool)
}
type PracticeAssessor interface {
	Manifest() AssessorManifest
	Assess(context.Context, EvidenceView, BoundedResearcher) ([]AssessmentDraft, error)
}

type AssessmentDraft struct {
	PracticeKey                                                string
	CriteriaVersion                                            int
	AssessorKey                                                string
	AssessorVersion                                            int
	SubjectKey                                                 string
	Status                                                     AssessmentStatus
	ResultIDs, PromptIDs, CheckedSources                       []string
	Explanation                                                string
	Reach, Persistence, EvidenceQuality, Actionability, Effort int
	PayloadVersion                                             int
	Payload                                                    json.RawMessage
}

type BlockType string

const (
	BlockText         BlockType = "TEXT"
	BlockMetric       BlockType = "METRIC"
	BlockLink         BlockType = "LINK"
	BlockQuestionList BlockType = "QUESTION_LIST"
	BlockEvidenceList BlockType = "EVIDENCE_LIST"
	BlockNotice       BlockType = "NOTICE"
)

type PresentationBlock struct {
	Type      BlockType `json:"type"`
	Title     string    `json:"title,omitempty"`
	Text      string    `json:"text,omitempty"`
	Value     string    `json:"value,omitempty"`
	URL       string    `json:"url,omitempty"`
	Items     []string  `json:"items,omitempty"`
	ResultIDs []string  `json:"result_ids,omitempty"`
}
type Presentation struct {
	Title, Summary, Effort string
	Blocks                 []PresentationBlock
}
// Presenter renders an eligible assessment into standard typed blocks. The
// catalog-driven presenter is the only implementation; the interface is the
// seam for a future practice that needs bespoke rendering.
type Presenter interface {
	Present(AssessmentDraft) (Presentation, error)
}

type Opportunity struct {
	ID, PracticeKey, SubjectKey string
	Assessment                  AssessmentDraft
}
type OutcomeObservation struct {
	Key        string
	ObservedAt time.Time
	Payload    json.RawMessage
}
// OpportunityEvaluator appends later observations to a completed opportunity.
// The assessment-status evaluator is the only implementation; the interface is
// the seam for a real before/after outcome evaluator.
type OpportunityEvaluator interface {
	Evaluate(context.Context, Opportunity, EvidenceView) ([]OutcomeObservation, error)
}

type artifactView map[string]EvidenceArtifact

func (v artifactView) Artifact(key string) (EvidenceArtifact, bool) { a, ok := v[key]; return a, ok }
func NewEvidenceView(artifacts []EvidenceArtifact) EvidenceView {
	v := artifactView{}
	for _, a := range artifacts {
		v[a.CollectorKey] = a
	}
	return v
}

func ValidateDraft(d AssessmentDraft, m AssessorManifest) error {
	practice, ok := Practice(d.PracticeKey)
	if !ok || practice.CriteriaVersion != d.CriteriaVersion {
		return errors.New("unknown practice or criteria version")
	}
	if d.AssessorKey != m.Key || d.AssessorVersion != m.ModuleVersion {
		return errors.New("assessor provenance mismatch")
	}
	if strings.TrimSpace(d.SubjectKey) == "" {
		return errors.New("subject key is required")
	}
	switch d.Status {
	case StatusMet, StatusPartial, StatusNotMet, StatusUnknown, StatusNotApplicable:
	default:
		return errors.New("invalid assessment status")
	}
	if d.PayloadVersion < 1 || !json.Valid(d.Payload) {
		return errors.New("invalid versioned payload")
	}
	return nil
}

func ValidatePresentation(p Presentation) error {
	if strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Summary) == "" {
		return errors.New("presentation title and summary are required")
	}
	for _, b := range p.Blocks {
		switch b.Type {
		case BlockText, BlockMetric, BlockQuestionList, BlockEvidenceList, BlockNotice:
		case BlockLink:
			u, err := url.Parse(b.URL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return fmt.Errorf("invalid presentation link")
			}
		default:
			return fmt.Errorf("invalid block type %q", b.Type)
		}
	}
	return nil
}

func Eligible(d AssessmentDraft) bool {
	return d.Status == StatusPartial || d.Status == StatusNotMet
}

// Rank orders unmet assessments: active direct blockers first, then reach,
// persistence, evidence quality, actionability, lower effort, and finally the
// stable practice and subject keys. Which practices block is catalog data, so
// the ranker never names one.
func Rank(drafts []AssessmentDraft) {
	blocking := map[string]bool{}
	for _, d := range drafts {
		if p, ok := Practice(d.PracticeKey); ok && p.DirectBlocker {
			blocking[d.PracticeKey] = true
		}
	}
	sort.SliceStable(drafts, func(i, j int) bool {
		a, b := drafts[i], drafts[j]
		ab, bb := blocking[a.PracticeKey] && a.Status == StatusNotMet, blocking[b.PracticeKey] && b.Status == StatusNotMet
		if ab != bb {
			return ab
		}
		av := []int{a.Reach, a.Persistence, a.EvidenceQuality, a.Actionability, -a.Effort}
		bv := []int{b.Reach, b.Persistence, b.EvidenceQuality, b.Actionability, -b.Effort}
		for n := range av {
			if av[n] != bv[n] {
				return av[n] > bv[n]
			}
		}
		if a.PracticeKey != b.PracticeKey {
			return a.PracticeKey < b.PracticeKey
		}
		return a.SubjectKey < b.SubjectKey
	})
}
