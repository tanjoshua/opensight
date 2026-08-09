package visibility

import (
	"context"
	"errors"
	"time"
)

// OwnedSiteScanPayloadVersion is the OwnedSiteScan contract version. Scans are
// only ever read inside the run that wrote them, so a bump needs no migration;
// it just fails closed against a stale in-flight payload.
const OwnedSiteScanPayloadVersion = 2

// MonitoringSnapshot is the answer corpus a finder reads: the latest analyzed
// runs and what was extracted from each response.
type MonitoringSnapshot struct {
	PayloadVersion int           `json:"payload_version"`
	BusinessName   string        `json:"business_name"`
	WebsiteHost    string        `json:"website_host"`
	Runs           []SnapshotRun `json:"runs"`
}

type SnapshotRun struct {
	RunID   string           `json:"run_id"`
	Results []SnapshotResult `json:"results"`
}

type SnapshotResult struct {
	ResultID string `json:"result_id"`
	PromptID string `json:"prompt_id"`
	Prompt   string `json:"prompt"`
	// ResponseText is the only unrecoverable field in the payload: mentions and
	// citations are extractions of it, so a finder that reads answers can only
	// ever be replayed over history that kept the answers.
	ResponseText string             `json:"response_text"`
	Mentioned    bool               `json:"mentioned"`
	Citations    []SnapshotCitation `json:"citations"`
}

// SnapshotCitation is one source the answer cited, carrying the businesses it
// was cited for. There is deliberately no answer-level competitor list here: the
// businesses an answer names and the businesses a source was cited for are
// different sets, and only the second is evidence about the source.
type SnapshotCitation struct {
	URL         string   `json:"url"`
	Domain      string   `json:"domain"`
	Passage     string   `json:"passage"`
	Competitors []string `json:"competitors"`
}

// PageFacts is what one parse of one page yields beyond its prose. Every field
// is read during the single walk the fetcher already performs, so a check built
// on it costs no extra request.
type PageFacts struct {
	URL             string   `json:"url"`
	Title           string   `json:"title,omitempty"`
	H1              string   `json:"h1,omitempty"`
	MetaDescription string   `json:"meta_description,omitempty"`
	Canonical       string   `json:"canonical,omitempty"`
	JSONLD          []string `json:"json_ld,omitempty"`
	HasTelLink      bool     `json:"has_tel_link,omitempty"`
	NoIndex         bool     `json:"noindex,omitempty"`
}

type OwnedSiteScan struct {
	PayloadVersion int      `json:"payload_version"`
	Host           string   `json:"host"`
	BusinessName   string   `json:"business_name"`
	CheckedURLs    []string `json:"checked_urls"`
	Text           string   `json:"text"`
	// Pages is ordered with the homepage first when it was retrievable, so a
	// check about "the homepage" reads Pages[0] rather than re-deriving it.
	Pages       []PageFacts `json:"pages,omitempty"`
	Reachable   bool        `json:"reachable"`
	AuthBarrier bool        `json:"auth_barrier"`
	NoIndex     bool        `json:"noindex"`
	NoIndexURLs []string    `json:"noindex_urls,omitempty"`
	// AgentDenied is keyed by lowercased crawler user agent. A key is absent
	// when robots.txt could not be inspected, which is how the per-agent checks
	// tell "allowed" apart from "unverifiable".
	AgentDenied    map[string]bool `json:"agent_denied,omitempty"`
	RobotsFound    bool            `json:"robots_found"`
	RobotsSitemaps []string        `json:"robots_sitemaps,omitempty"`
	SitemapFound   bool            `json:"sitemap_found"`
	// FetchFailure and RobotsFailure are kept apart so one unreadable robots.txt
	// does not turn every unrelated check on the page into "could not verify".
	FetchFailure  string `json:"fetch_failure,omitempty"`
	RobotsFailure string `json:"robots_failure,omitempty"`
}

// ResearchURLBudget is how many third-party pages one run may read, shared
// across every finder. It is a worst-case bound on serial third-party fetches
// and the SSRF surface a run exposes — not the ordinary constraint on how much
// listings work gets recommended, which maxURLsPerCandidate keeps well under
// it. It is defined here so the fixture harness scores finders under exactly
// the budget production grants them, and a listing that sits past it is
// genuinely missed rather than silently found.
const ResearchURLBudget = 20

// maxURLsPerCandidate caps how many of one domain's cited URLs a finder may
// inspect. Two pages are enough to confirm absence, and no single candidate
// may monopolise the run's shared research budget.
const maxURLsPerCandidate = 2

// ErrResearchBudget is returned when the per-run URL budget is spent. It is a
// distinct error because exhausting the budget means "we did not look", which a
// finder must never treat as "we looked and found nothing".
var ErrResearchBudget = errors.New("research URL budget exhausted")

type ResearchRequest struct{ URL string }

type ResearchResult struct {
	URL, Text string
	CheckedAt time.Time
}

// BoundedResearcher fetches a third-party page under an SSRF-safe, budgeted
// client. Exhausting the budget is an error, not a silent empty result.
type BoundedResearcher interface {
	Inspect(context.Context, ResearchRequest) (ResearchResult, error)
}
