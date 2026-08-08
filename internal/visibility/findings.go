package visibility

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
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
}

const (
	SourceSiteAudit   = "site-audit"
	SourceCitationGap = "citation-gap"
)

type FinderInput struct {
	Audit    []CheckResult
	Snapshot MonitoringSnapshot
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
	out := []Finding{}
	for _, result := range in.Audit {
		if result.Outcome != CheckFail {
			continue
		}
		check, ok := CheckByKey(result.Key)
		if !ok || check.Informational || len(check.Fix) == 0 {
			continue
		}
		out = append(out, Finding{
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
		})
	}
	return out, nil
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
	results     []string
	urls        []string
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
				spelling := strings.ToLower(strings.TrimSpace(citation.Domain))
				domain := normalizeHost(spelling)
				if domain == "" || domain == normalizeHost(snapshot.WebsiteHost) {
					continue
				}
				evidence := byDomain[domain]
				if evidence == nil {
					evidence = &domainEvidence{spellings: map[string]bool{}, prompts: map[string]bool{}, runs: map[string]bool{}, competitors: map[string]bool{}}
					byDomain[domain] = evidence
				}
				evidence.spellings[spelling] = true
				evidence.prompts[result.PromptID] = true
				evidence.runs[run.RunID] = true
				evidence.results = append(evidence.results, result.ResultID)
				for _, competitor := range citation.Competitors {
					if trimmed := strings.TrimSpace(competitor); trimmed != "" {
						evidence.competitors[trimmed] = true
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

	out := []Finding{}
	for _, evidence := range candidates {
		if len(evidence.competitors) == 0 {
			continue
		}
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
		out = append(out, findingForDomain(evidence, checked))
	}
	return out, nil
}

func findingForDomain(evidence *domainEvidence, checked []string) Finding {
	competitors := mapKeys(evidence.competitors)
	questions := len(evidence.prompts)
	results := dedupe(evidence.results)
	return Finding{
		Key:      SourceCitationGap + ":" + evidence.domain(),
		Source:   SourceCitationGap,
		Category: CategoryListings,
		Title:    "Get listed on " + evidence.domain(),
		Body: fmt.Sprintf(
			"ChatGPT cited %s while answering %d of your monitored %s without naming you. It cited that source for %s, and the %d %s we read there %s mention your business.",
			evidence.domain(),
			questions,
			plural(questions, "question", "questions"),
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
		Detail:    fmt.Sprintf("ChatGPT cited this source for %d of the businesses it recommended instead: %s.", len(competitors), strings.Join(competitors, ", ")),
		ResultIDs: results,
		PromptIDs: mapKeys(evidence.prompts),
		Sources:   checked,
		Reach:     len(results),
		Priority:  1,
	}
}

// confirmAbsent reads the cited pages and reports which were readable, whether
// any already lists the business, and whether the budget ran out before the
// candidates did. Spending the budget before recommending work is what keeps
// the advice trustworthy — and an exhausted budget means the remaining pages
// were never looked at, which is not evidence of absence.
func confirmAbsent(ctx context.Context, research BoundedResearcher, urls []string, businessName string) (checked []string, listed, exhausted bool) {
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
		checked = append(checked, result.URL)
		if listedOnPage(result.Text, businessName) {
			return checked, true, false
		}
	}
	return checked, false, false
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
