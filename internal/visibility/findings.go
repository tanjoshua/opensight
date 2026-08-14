package visibility

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"opensight/internal/llm"
)

// Finding is one piece of work, discovered from evidence. Unlike a check, the
// set of findings is open: a finder produces what the evidence supports this
// week, and a finding the evidence stops producing simply stops appearing.
type Finding struct {
	// Key is stable and readable ("site-audit:pages_allow_indexing",
	// "citation-gap:healthhub.sg"). It is the identity the lifecycle hangs on:
	// the same key next week is the same finding.
	Key    string `json:"key"`
	Source string `json:"source"`
	// Category is the kind of change the finding asks for, and the axis the work
	// queue is filtered on. Source is where the finding came from; category is
	// what the user has to go and do about it.
	Category  string   `json:"category"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Steps     []string `json:"steps,omitempty"`
	Detail    string   `json:"detail,omitempty"`
	ResultIDs []string `json:"result_ids,omitempty"`
	PromptIDs []string `json:"prompt_ids,omitempty"`
	Sources   []string `json:"sources,omitempty"`
	// Blocking marks work that stops everything else from mattering. Reach is
	// how many monitored answers the finding affects; a site-wide finding has no
	// per-answer evidence and leaves it zero. Priority breaks ties within a
	// source. All three are evidence-derived, never hand-set constants.
	Blocking bool `json:"blocking"`
	Reach    int  `json:"reach"`
	Priority int  `json:"priority"`
	// Comparison is the two sides of the case for the work, for findings that
	// have both. A site-audit finding leaves it empty.
	Comparison Comparison `json:"comparison,omitzero"`
}

// Comparison is why a content finding is worth doing, in the only two pieces of
// evidence that argue it: what the answer engine actually wrote about a
// competitor, and what the customer's own site says on the same subject. Both
// are quotations rather than summaries — the product's claim is that a
// recommendation went elsewhere, and a quote is the only form of that claim the
// user can check for themselves.
type Comparison struct {
	// Coverage is the model's verdict on the customer's own site: "absent" or
	// "partial". Anything else is treated as unknown and simply not rendered.
	Coverage string `json:"coverage,omitempty"`
	// Cited are passages from monitored answers, each with the source the answer
	// cited alongside it.
	Cited []CitedQuote `json:"cited,omitempty"`
	// Site are passages from the customer's own pages, confirmed to appear in
	// the crawled text before they are stored.
	Site []string `json:"site,omitempty"`
}

// Empty reports whether there is nothing to compare, which is the case for
// every finding that is not derived from competitor evidence.
func (c Comparison) Empty() bool {
	return c.Coverage == "" && len(c.Cited) == 0 && len(c.Site) == 0
}

// CitedQuote is one passage an answer used to recommend somebody else, and the
// source it cited for it.
type CitedQuote struct {
	Quote  string `json:"quote"`
	Domain string `json:"domain"`
}

const (
	SourceSiteAudit         = "site-audit"
	SourceCitationGap       = "citation-gap"
	SourceCompetitorContent = "competitor-content"
)

type FinderInput struct {
	Audit    []CheckResult
	Snapshot MonitoringSnapshot
	// Profile is the confirmed business profile. It lets a finding say what to
	// publish rather than what kind of thing to publish, using data the user has
	// already reviewed.
	Profile                   SiteProfile
	SiteContent               string
	PriorContentOpportunities []llm.PriorContentOpportunity
	Classifier                llm.SourceClassifier
}

// Finder turns evidence into findings. Adding advice is one implementation
// plus one entry in Finders — there is no catalog, registry or manifest to
// keep in step, because a finding describes itself.
type Finder interface {
	Key() string
	Find(context.Context, FinderInput, BoundedResearcher) ([]Finding, error)
}

func Finders() []Finder { return []Finder{siteAuditFinder{}, citationGapFinder{}} }

// Rank orders findings: blockers first, then by category, then by how many
// answers they affect, then by catalog priority, then by stable key.
//
// Category outranks reach deliberately. A listing on a third party's directory
// can affect more answers than any single site change and still be the work a
// business is least able to finish, since it depends on somebody else accepting
// the entry. Reach then orders the listings against each other, where it is a
// fair comparison.
func Rank(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Blocking != b.Blocking {
			return a.Blocking
		}
		if ao, bo := CategoryOrder(a.Category), CategoryOrder(b.Category); ao != bo {
			return ao < bo
		}
		if a.Reach != b.Reach {
			return a.Reach > b.Reach
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.Key < b.Key
	})
}

// siteAuditFinder turns every failing check that declares a fix into work. A
// check with no Fix is still reported on the checklist; it just never becomes
// a task, which is how the catalog decides what is worth a user's attention.
type siteAuditFinder struct{}

func (siteAuditFinder) Key() string { return SourceSiteAudit }

func (siteAuditFinder) Find(_ context.Context, in FinderInput, _ BoundedResearcher) ([]Finding, error) {
	failed := map[string]bool{}
	for _, result := range in.Audit {
		if result.Outcome == CheckFail {
			failed[result.Key] = true
		}
	}
	out := []Finding{}
	for _, result := range in.Audit {
		if result.Outcome != CheckFail {
			continue
		}
		check, ok := CheckByKey(result.Key)
		if !ok || check.Informational || len(check.Fix) == 0 {
			continue
		}
		// A failure entailed by its parent's failure is not separate work: the
		// parent's fix is the only way to attempt it. It stays on the checklist
		// and folds into the parent's finding here.
		if check.DependsOn != "" && failed[check.DependsOn] {
			continue
		}
		finding := Finding{
			Key:      SourceSiteAudit + ":" + check.Key,
			Source:   SourceSiteAudit,
			Category: check.Group,
			Title:    check.FixTitle,
			Body:     check.What,
			Steps:    check.Fix,
			Detail:   result.Detail,
			Sources:  result.Sources,
			Blocking: check.Blocking,
			Priority: check.Priority,
		}
		if check.Key == CheckStructuredData {
			steps, detail := structuredDataSteps(in.Profile, foldedInto(check.Key, failed))
			if len(steps) > 0 {
				finding.Steps = steps
			}
			finding.Detail += detail
		}
		out = append(out, finding)
	}
	return out, nil
}

// foldedInto returns the failed checks the given parent absorbed, in catalog
// order.
func foldedInto(parent string, failed map[string]bool) []Check {
	out := []Check{}
	for _, check := range catalog {
		if check.DependsOn == parent && failed[check.Key] {
			out = append(out, check)
		}
	}
	return out
}

// citationGapFinder reads the answer corpus for sources that shape answers the
// business is missing from. The signal that earns a recommendation is competitor
// lift: a source ChatGPT cited when recommending other businesses is one worth
// being on. Lift counts only the businesses a source was cited *for* (05's
// attribution), never every business the answer happened to name — the second is
// a far larger set that says nothing about the source. Presence is then
// confirmed by reading the page, so the product never tells someone to get
// listed where they already are.
type citationGapFinder struct{}

func (citationGapFinder) Key() string { return SourceCitationGap }

type domainEvidence struct {
	// spellings holds every host form the corpus recorded for this one source.
	// Grouping is done on the www-folded host, so a directory cited as both
	// yelp.com and www.yelp.com is one source with two spellings rather than two
	// sources that each miss the recurrence gate.
	spellings   map[string]bool
	prompts     map[string]bool
	runs        map[string]bool
	competitors map[string]bool
	results     map[string]bool
	urls        []string
	claims      []llm.SourceClaim
}

type inspectedDomain struct {
	evidence *domainEvidence
	pages    []ResearchResult
}

// domain is the spelling shown to the user: the plainest form the corpus
// actually recorded, so an apex host wins over its www variant but a source
// only ever seen with www keeps it.
func (d *domainEvidence) domain() string {
	best := ""
	for spelling := range d.spellings {
		if best == "" || len(spelling) < len(best) || (len(spelling) == len(best) && spelling < best) {
			best = spelling
		}
	}
	return best
}

func (citationGapFinder) Find(ctx context.Context, in FinderInput, research BoundedResearcher) ([]Finding, error) {
	snapshot := in.Snapshot
	byDomain := map[string]*domainEvidence{}
	for _, run := range snapshot.Runs {
		for _, result := range run.Results {
			// Only answers the business is missing from can show a gap. An answer
			// that already names the business is evidence the source is working.
			if result.Mentioned {
				continue
			}
			for _, citation := range result.Citations {
				// Recurrence, reach, and inspection are link-specific: an unlinked
				// citation occurrence is not evidence that this source supported a
				// competitor and cannot inflate any of those counts.
				if len(citation.Competitors) == 0 {
					continue
				}
				spelling := strings.ToLower(strings.TrimSpace(citation.Domain))
				domain := normalizeHost(spelling)
				if domain == "" || domain == normalizeHost(snapshot.WebsiteHost) {
					continue
				}
				evidence := byDomain[domain]
				if evidence == nil {
					evidence = &domainEvidence{spellings: map[string]bool{}, prompts: map[string]bool{}, runs: map[string]bool{}, competitors: map[string]bool{}, results: map[string]bool{}}
					byDomain[domain] = evidence
				}
				evidence.spellings[spelling] = true
				evidence.prompts[result.PromptID] = true
				evidence.runs[run.RunID] = true
				evidence.results[result.ResultID] = true
				for _, competitor := range citation.Competitors {
					if trimmed := strings.TrimSpace(competitor); trimmed != "" {
						evidence.competitors[trimmed] = true
						if passage := strings.TrimSpace(citation.Passage); passage != "" {
							evidence.claims = append(evidence.claims, llm.SourceClaim{
								Owner: trimmed, Passage: passage, Question: result.Prompt,
								ResultID: result.ResultID, PromptID: result.PromptID,
							})
						}
					}
				}
				if parsed, err := url.Parse(citation.URL); err == nil && normalizeHost(parsed.Hostname()) == domain {
					evidence.urls = append(evidence.urls, citation.URL)
				}
			}
		}
	}

	candidates := make([]*domainEvidence, 0, len(byDomain))
	for _, evidence := range byDomain {
		// One mention of a source proves nothing. It earns attention by recurring
		// across questions, or across weeks for the same question.
		if len(evidence.prompts) >= 2 || (len(evidence.prompts) == 1 && len(evidence.runs) >= 2) {
			candidates = append(candidates, evidence)
		}
	}
	// Highest competitor lift first, so a limited research budget is spent on the
	// sources most likely to be worth being on.
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i].competitors) != len(candidates[j].competitors) {
			return len(candidates[i].competitors) > len(candidates[j].competitors)
		}
		if len(candidates[i].prompts) != len(candidates[j].prompts) {
			return len(candidates[i].prompts) > len(candidates[j].prompts)
		}
		return candidates[i].domain() < candidates[j].domain()
	})

	inspected := []inspectedDomain{}
	for _, evidence := range candidates {
		urls := dedupe(evidence.urls)
		if len(urls) > maxURLsPerCandidate {
			urls = urls[:maxURLsPerCandidate]
		}
		checked, listed, exhausted := confirmAbsent(ctx, research, urls, snapshot.BusinessName)
		// Recommend work only when the pages were actually read and the business
		// genuinely is not on them. Nothing readable, an already-present listing,
		// or a spent budget all mean the evidence cannot support a recommendation.
		if len(checked) == 0 || listed || exhausted {
			continue
		}
		inspected = append(inspected, inspectedDomain{evidence: evidence, pages: checked})
	}
	if len(inspected) == 0 {
		return []Finding{}, nil
	}
	if in.Classifier == nil {
		return nil, errors.New("citation source classifier is required")
	}
	classificationInput := llm.SourceClassificationInput{
		BusinessName:              snapshot.BusinessName,
		SiteContent:               in.SiteContent,
		PriorContentOpportunities: in.PriorContentOpportunities,
		Candidates:                make([]llm.SourceCandidate, len(inspected)),
	}
	for i, candidate := range inspected {
		pages := make([]llm.SourcePage, len(candidate.pages))
		for j, page := range candidate.pages {
			pages[j] = llm.SourcePage{URL: page.URL, Content: pageTextForClassification(page.Text)}
		}
		classificationInput.Candidates[i] = llm.SourceCandidate{Domain: candidate.evidence.domain(), Pages: pages, Claims: dedupeClaims(candidate.evidence.claims)}
	}
	analysis, err := in.Classifier.ClassifySources(ctx, classificationInput)
	if err != nil {
		return nil, err
	}
	out := []Finding{}
	for _, classification := range analysis.Sources {
		candidate := inspected[classification.CandidateIndex]
		checked := make([]string, len(candidate.pages))
		for i, page := range candidate.pages {
			checked[i] = page.URL
		}
		if classification.Kind != llm.SourceCompetitorOwned {
			out = append(out, findingForDomain(candidate.evidence, checked))
		}
	}
	for _, opportunity := range analysis.Opportunities {
		out = append(out, competitorContentFinding(opportunity, inspected, classificationInput.Candidates, in.SiteContent))
	}
	return out, nil
}

func findingForDomain(evidence *domainEvidence, checked []string) Finding {
	competitors := mapKeys(evidence.competitors)
	results := mapKeys(evidence.results)
	return Finding{
		Key:      SourceCitationGap + ":" + evidence.domain(),
		Source:   SourceCitationGap,
		Category: CategoryListings,
		Title:    "Get listed on " + evidence.domain(),
		Body: fmt.Sprintf(
			"In %d answers that omitted you, ChatGPT cited %s to support recommendations of %s. The %d %s we read there %s mention your business.",
			len(results),
			evidence.domain(),
			nameList(competitors),
			len(checked),
			plural(len(checked), "page", "pages"),
			plural(len(checked), "does not", "do not"),
		),
		Steps: []string{
			"Open the pages we checked below and find how a business gets added — a listing form, a claim link, or an editor to contact.",
			"Submit a complete, accurate entry: name, address, phone and hours exactly as they appear on your own site.",
			"Never offer an incentive for a listing or a review, and make no comparative claims about the businesses already there.",
			"A later weekly check will confirm once your business appears on the page.",
		},
		Detail:    fmt.Sprintf("Linked competitor names extracted from those citations: %s.", strings.Join(competitors, ", ")),
		ResultIDs: results,
		PromptIDs: mapKeys(evidence.prompts),
		Sources:   checked,
		Reach:     len(results),
		Priority:  1,
	}
}

func competitorContentFinding(opportunity llm.ContentOpportunity, inspected []inspectedDomain, candidates []llm.SourceCandidate, siteContent string) Finding {
	results := map[string]bool{}
	prompts := map[string]bool{}
	sources := map[string]bool{}
	byDomain := map[string][]string{}
	order := []string{}
	for _, ref := range opportunity.Evidence {
		claim := candidates[ref.CandidateIndex].Claims[ref.ClaimIndex]
		domain := candidates[ref.CandidateIndex].Domain
		results[claim.ResultID] = true
		prompts[claim.PromptID] = true
		for _, page := range inspected[ref.CandidateIndex].pages {
			sources[page.URL] = true
		}
		if _, seen := byDomain[domain]; !seen {
			order = append(order, domain)
		}
		byDomain[domain] = append(byDomain[domain], claim.Passage)
	}
	domainCount := len(order)
	return Finding{
		Key: SourceCompetitorContent + ":" + opportunity.Key, Source: SourceCompetitorContent,
		Category: CategoryContent, Title: opportunity.Title,
		Body:   strings.TrimSpace(opportunity.Observation + " " + opportunity.SiteState),
		Steps:  []string{opportunity.SuggestedAction},
		Detail: fmt.Sprintf("Observed across %d competitor-owned %s.", domainCount, plural(domainCount, "source", "sources")),
		Comparison: Comparison{
			Coverage: opportunity.Coverage,
			Cited:    citedQuotes(order, byDomain),
			Site:     siteQuotes(opportunity.SiteEvidence, siteContent),
		},
		ResultIDs: mapKeys(results), PromptIDs: mapKeys(prompts), Sources: mapKeys(sources),
		Reach: len(results), Priority: 1,
	}
}

const (
	// maxCitedQuotes caps the competitor side of a comparison. Three shows the
	// pattern recurred without turning a card into a transcript, and the full set
	// of answers is always one click away in the evidence drawer.
	maxCitedQuotes = 3
	// maxSiteQuotes caps the customer's own side, which only has to establish
	// what they already say — one or two passages settle that.
	maxSiteQuotes = 2
	// maxQuoteRunes trims a passage that would dominate the card. Citation spans
	// run to a few hundred characters, so this keeps most of them whole.
	maxQuoteRunes = 400
)

// citedQuotes picks the passages to show, taking one domain at a time before
// taking a second from any of them. A finding whose detail says the pattern
// recurred across three sources must not then quote the same source three
// times: breadth is the claim, so breadth is what the evidence shows.
func citedQuotes(order []string, byDomain map[string][]string) []CitedQuote {
	out := []CitedQuote{}
	seen := map[string]bool{}
	for round := 0; len(out) < maxCitedQuotes; round++ {
		exhausted := true
		for _, domain := range order {
			passages := byDomain[domain]
			if round >= len(passages) {
				continue
			}
			exhausted = false
			quote := trimQuote(passages[round])
			if quote == "" || seen[quote] {
				continue
			}
			seen[quote] = true
			if out = append(out, CitedQuote{Quote: quote, Domain: domain}); len(out) == maxCitedQuotes {
				return out
			}
		}
		if exhausted {
			break
		}
	}
	return out
}

// siteQuotes keeps only the passages that really do appear in the crawled text.
// The model is told to copy them exactly, but its output is never validated, and
// a quote the card attributes to the customer's own page has to be one they can
// go and find there.
//
// The comparison is on letters and digits alone. Extracting text from markup
// leaves punctuation attached to whatever inline element held it — a real page
// yielded "for root canal treatment ." where the model, reading the same text,
// wrote "treatment." — and discarding a genuine quote over one space is worse
// than the false positive this risks, which would require the customer's own
// site to contain the whole passage contiguously anyway. The stored string stays
// the model's, since it is the same words with the extraction artifacts removed.
func siteQuotes(claimed []string, siteContent string) []string {
	haystack := normalizeQuote(siteContent)
	if haystack == "" {
		return nil
	}
	out := []string{}
	for _, passage := range claimed {
		normalized := normalizeQuote(passage)
		if normalized == "" || !strings.Contains(haystack, normalized) {
			continue
		}
		out = append(out, trimQuote(passage))
		if len(out) == maxSiteQuotes {
			break
		}
	}
	return out
}

// spaceBeforePunctuation matches the gap extraction leaves when a sentence's
// closing mark sits in its own inline element, which yields "treatment ." from
// markup a reader sees as "treatment.".
var spaceBeforePunctuation = regexp.MustCompile(`\s+([.,;:!?])`)

// trimQuote renders a passage as one line of quotable prose: collapsed
// whitespace, no leading list marker, no extraction artifact before a
// punctuation mark, and cut on a word boundary if it is long.
func trimQuote(passage string) string {
	quote := strings.TrimLeft(strings.Join(strings.Fields(passage), " "), "-*•– ")
	quote = spaceBeforePunctuation.ReplaceAllString(quote, "$1")
	runes := []rune(quote)
	if len(runes) <= maxQuoteRunes {
		return quote
	}
	cut := string(runes[:maxQuoteRunes])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:—-") + "…"
}

func normalizeQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// confirmAbsent reads the cited pages and reports which were readable, whether
// any already lists the business, and whether the budget ran out before the
// candidates did. Spending the budget before recommending work is what keeps
// the advice trustworthy — and an exhausted budget means the remaining pages
// were never looked at, which is not evidence of absence.
func confirmAbsent(ctx context.Context, research BoundedResearcher, urls []string, businessName string) (checked []ResearchResult, listed, exhausted bool) {
	if research == nil || strings.TrimSpace(businessName) == "" {
		return nil, false, false
	}
	for _, raw := range urls {
		result, err := research.Inspect(ctx, ResearchRequest{URL: raw})
		if errors.Is(err, ErrResearchBudget) {
			return checked, false, true
		}
		if err != nil {
			continue
		}
		checked = append(checked, result)
		if listedOnPage(result.Text, businessName) {
			return checked, true, false
		}
	}
	return checked, false, false
}

func dedupeClaims(in []llm.SourceClaim) []llm.SourceClaim {
	seen := map[string]bool{}
	out := []llm.SourceClaim{}
	for _, claim := range in {
		key := claim.Owner + "\x00" + claim.Passage + "\x00" + claim.ResultID
		if !seen[key] {
			seen[key] = true
			out = append(out, claim)
		}
	}
	return out
}

// nameList renders competitor names for a sentence, capped so one finding body
// cannot become a list dump. The full set is always in the finding's detail.
func nameList(names []string) string {
	const maxListed = 3
	switch {
	case len(names) == 0:
		return "other businesses"
	case len(names) == 1:
		return names[0]
	case len(names) <= maxListed:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	default:
		rest := len(names) - maxListed
		return fmt.Sprintf("%s and %d %s", strings.Join(names[:maxListed], ", "), rest, plural(rest, "other", "others"))
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
