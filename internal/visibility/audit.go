package visibility

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Audit evaluates the whole catalog against one site scan and returns exactly
// one result per catalog check, in catalog order. A scan that read nothing
// reports every check unverifiable rather than asserting failures the evidence
// cannot support.
func Audit(s OwnedSiteScan) []CheckResult {
	if s.FetchFailure != "" {
		return allUnverified("The website could not be inspected: " + s.FetchFailure)
	}

	// Access is answered by the response itself — status codes, headers and
	// robots.txt — so it stays answerable even for a scan that captured no page
	// markup. Everything else is read out of the markup and is unverifiable
	// without it.
	byKey := map[string]CheckResult{
		CheckSiteReachable: checkIf(CheckSiteReachable, s.Reachable,
			fmt.Sprintf("Read %d %s, starting from the homepage.", pageCount(s), plural(pageCount(s), "page", "pages")),
			"The homepage did not return usable content."),
		CheckNoLoginWall: checkIf(CheckNoLoginWall, !s.AuthBarrier,
			"No page we requested asked us to sign in.",
			"The homepage answered with an authentication barrier instead of its content."),
		CheckPagesAllowIndexing: indexingCheck(s),
		CheckRobotsOAISearchBot: robotsCheck(CheckRobotsOAISearchBot, AgentOAISearchBot, "OAI-SearchBot", s),
		CheckRobotsChatGPTUser:  robotsCheck(CheckRobotsChatGPTUser, AgentChatGPTUser, "ChatGPT-User", s),
		CheckRobotsGPTBot:       gptBotCheck(s),
	}
	if len(s.Pages) == 0 {
		for _, c := range catalog {
			if _, answered := byKey[c.Key]; !answered {
				byKey[c.Key] = unverified(c.Key, "No page markup could be read, so this could not be checked.")
			}
		}
		return inCatalogOrder(byKey)
	}

	home := s.Pages[0]
	ld := collectJSONLD(s.Pages)
	hasTel, telSource := ld.HasTelephone, "structured data"
	if !hasTel {
		for _, page := range s.Pages {
			if page.HasTelLink {
				hasTel, telSource = true, "a tel: link"
				break
			}
		}
	}

	for key, result := range map[string]CheckResult{
		CheckSitemapPublished: sitemapCheck(s),
		CheckRobotsSitemap:    robotsSitemapCheck(s),
		CheckCanonicalURL: checkIf(CheckCanonicalURL, home.Canonical != "",
			"The homepage declares a canonical URL: "+home.Canonical,
			"The homepage has no link rel=canonical tag, so duplicate addresses cannot be resolved to one page."),
		CheckTitlesUnique:       titlesUniqueCheck(s.Pages),
		CheckTitleNamesBusiness: titleNamesBusinessCheck(home, s.BusinessName),
		CheckMetaDescription: checkIf(CheckMetaDescription, strings.TrimSpace(home.MetaDescription) != "",
			"The homepage describes itself as: "+quoted(home.MetaDescription),
			"The homepage has no meta description."),

		CheckStructuredData: checkIf(CheckStructuredData, ld.Parsed,
			fmt.Sprintf("Found structured data declaring %s.", strings.Join(ld.Types, ", ")),
			"No page we read publishes a readable application/ld+json block."),
		CheckStructuredType: checkIf(CheckStructuredType, ld.BusinessType != "",
			"Structured data declares the business as "+ld.BusinessType+".",
			"Structured data declares no Organization or LocalBusiness type."),
		CheckStructuredTelephone: checkIf(CheckStructuredTelephone, hasTel,
			"A phone number is machine-readable from "+telSource+".",
			"No phone number was found in structured data or as a tel: link."),
		CheckStructuredAddress: checkIf(CheckStructuredAddress, ld.HasAddress,
			"Structured data includes a postal address.",
			"Structured data includes no postal address."),
		CheckStructuredOpenHours: checkIf(CheckStructuredOpenHours, ld.HasOpenHours,
			"Structured data includes opening hours.",
			"Structured data includes no opening hours."),
	} {
		byKey[key] = result
	}
	return inCatalogOrder(byKey)
}

func allUnverified(detail string) []CheckResult {
	out := make([]CheckResult, 0, len(catalog))
	for _, c := range catalog {
		out = append(out, unverified(c.Key, detail))
	}
	return out
}

// inCatalogOrder emits one result per catalog check, in catalog order rather
// than map order, which is what makes the audit deterministic. A check the
// evaluation somehow missed reports as unverifiable rather than disappearing.
func inCatalogOrder(byKey map[string]CheckResult) []CheckResult {
	out := make([]CheckResult, 0, len(catalog))
	for _, c := range catalog {
		result, ok := byKey[c.Key]
		if !ok {
			result = unverified(c.Key, "This check did not run.")
		}
		out = append(out, result)
	}
	return out
}

// PassedOf counts non-informational results, which is what the checklist's
// "4 of 6 checks passed" summary reports. Counts, never a ratio or a score.
func PassedOf(results []CheckResult) (passed, total int) {
	for _, r := range results {
		if c, ok := CheckByKey(r.Key); ok && c.Informational {
			continue
		}
		total++
		if r.Outcome == CheckPass {
			passed++
		}
	}
	return passed, total
}

// pass, fail, unverified and notApplicable build a CheckResult. Every check
// states its evidence, so the detail argument is never optional.
func pass(key, detail string) CheckResult {
	return CheckResult{Key: key, Outcome: CheckPass, Detail: detail}
}

func fail(key, detail string) CheckResult {
	return CheckResult{Key: key, Outcome: CheckFail, Detail: detail}
}

func unverified(key, detail string) CheckResult {
	return CheckResult{Key: key, Outcome: CheckCouldNotVerify, Detail: detail}
}

func notApplicable(key, detail string) CheckResult {
	return CheckResult{Key: key, Outcome: CheckNotApplicable, Detail: detail}
}

// checkIf is the common shape: one condition, one sentence either way.
func checkIf(key string, ok bool, passDetail, failDetail string) CheckResult {
	if ok {
		return pass(key, passDetail)
	}
	return fail(key, failDetail)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// quoted renders site copy back to its owner, capped so one detail line cannot
// become a dump. Quoting what was actually read is what distinguishes evidence
// from a restatement of the check's own title.
func quoted(text string) string {
	const maxRunes = 110
	trimmed := strings.TrimSpace(text)
	if runes := []rune(trimmed); len(runes) > maxRunes {
		trimmed = strings.TrimSpace(string(runes[:maxRunes])) + "…"
	}
	return "“" + trimmed + "”"
}

// pathsOf renders URLs as their paths, which is how a site owner recognises
// their own pages. It caps the list so one detail line cannot become a dump.
func pathsOf(urls []string) string {
	const maxListed = 4
	out := make([]string, 0, len(urls))
	for _, raw := range urls {
		path := raw
		if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" {
			path = parsed.Path
		}
		out = append(out, path)
	}
	if len(out) > maxListed {
		return strings.Join(out[:maxListed], ", ") + fmt.Sprintf(" and %d more", len(out)-maxListed)
	}
	return strings.Join(out, ", ")
}

// pageCount is how many pages the scan actually read. CheckedURLs also carries
// robots.txt, so it must never be used where the detail says "pages".
func pageCount(s OwnedSiteScan) int {
	if len(s.Pages) > 0 {
		return len(s.Pages)
	}
	return len(s.CheckedURLs)
}

// robotsCheck turns one crawler's robots.txt verdict into a result. An absent
// key means robots.txt was never read, which is not the same as permission.
func robotsCheck(key, agent, label string, s OwnedSiteScan) CheckResult {
	denied, known := s.AgentDenied[agent]
	n := pageCount(s)
	switch {
	case !known:
		return unverified(key, "robots.txt could not be inspected: "+s.RobotsFailure)
	case denied:
		return fail(key, fmt.Sprintf("robots.txt disallows %s on at least one of the %d %s checked.", label, n, plural(n, "page", "pages")))
	case !s.RobotsFound:
		return pass(key, fmt.Sprintf("No robots.txt is published, so nothing is disallowed to %s.", label))
	default:
		return pass(key, fmt.Sprintf("robots.txt is published and no rule disallows %s on the %d %s checked.", label, n, plural(n, "page", "pages")))
	}
}

// indexingCheck reports the site-level indexing verdict without contradicting
// the page list behind it: a site stays indexable when individual pages opt out,
// and the detail has to say which ones rather than claim there are none.
func indexingCheck(s OwnedSiteScan) CheckResult {
	n := pageCount(s)
	switch {
	case s.NoIndex:
		return fail(CheckPagesAllowIndexing, fmt.Sprintf("%d of %d pages carry a noindex directive: %s", len(s.NoIndexURLs), n, pathsOf(s.NoIndexURLs)))
	case len(s.NoIndexURLs) > 0:
		return pass(CheckPagesAllowIndexing, fmt.Sprintf("The site is indexable. %d of %d pages opt out individually (%s), which does not block the rest.", len(s.NoIndexURLs), n, pathsOf(s.NoIndexURLs)))
	default:
		return pass(CheckPagesAllowIndexing, fmt.Sprintf("None of the %d %s we read carries a noindex directive.", n, plural(n, "page", "pages")))
	}
}

// gptBotCheck reports the training crawler as a fact rather than a shortfall:
// blocking GPTBot is a legitimate choice and must never read as a failure.
func gptBotCheck(s OwnedSiteScan) CheckResult {
	denied, known := s.AgentDenied[AgentGPTBot]
	switch {
	case !known:
		return unverified(CheckRobotsGPTBot, "robots.txt could not be inspected: "+s.RobotsFailure)
	case denied:
		return notApplicable(CheckRobotsGPTBot, "robots.txt blocks GPTBot. This only affects model training, not ChatGPT search, and blocking it is a valid choice.")
	default:
		return pass(CheckRobotsGPTBot, "robots.txt allows GPTBot, OpenAI's model-training crawler.")
	}
}

func sitemapCheck(s OwnedSiteScan) CheckResult {
	if s.SitemapFound {
		return pass(CheckSitemapPublished, "/sitemap.xml answered with usable XML.")
	}
	return fail(CheckSitemapPublished, "/sitemap.xml did not answer with usable XML, so crawlers must discover pages by following links.")
}

func robotsSitemapCheck(s OwnedSiteScan) CheckResult {
	switch {
	case s.RobotsFailure != "":
		return unverified(CheckRobotsSitemap, "robots.txt could not be inspected: "+s.RobotsFailure)
	case len(s.RobotsSitemaps) > 0:
		return pass(CheckRobotsSitemap, "robots.txt declares "+strings.Join(s.RobotsSitemaps, ", "))
	case !s.RobotsFound:
		return fail(CheckRobotsSitemap, "No robots.txt is published, so it cannot point to a sitemap.")
	default:
		return fail(CheckRobotsSitemap, "robots.txt is published but declares no Sitemap: directive.")
	}
}

func titlesUniqueCheck(pages []PageFacts) CheckResult {
	titled, byTitle := 0, map[string][]string{}
	for _, page := range pages {
		title := strings.ToLower(strings.TrimSpace(page.Title))
		if title == "" {
			continue
		}
		titled++
		byTitle[title] = append(byTitle[title], page.URL)
	}
	if titled == 0 {
		return fail(CheckTitlesUnique, fmt.Sprintf("None of the %d %s we read has a title.", len(pages), plural(len(pages), "page", "pages")))
	}
	for title, urls := range byTitle {
		if len(urls) > 1 {
			sort.Strings(urls)
			return fail(CheckTitlesUnique, fmt.Sprintf("%d pages share the title %q: %s", len(urls), title, pathsOf(urls)))
		}
	}
	return pass(CheckTitlesUnique, fmt.Sprintf("All %d %s we read has a distinct title.", titled, plural(titled, "page", "pages")))
}

func titleNamesBusinessCheck(home PageFacts, businessName string) CheckResult {
	switch {
	case strings.TrimSpace(businessName) == "":
		return notApplicable(CheckTitleNamesBusiness, "No confirmed business name is available to compare the title against.")
	case strings.TrimSpace(home.Title) == "":
		return fail(CheckTitleNamesBusiness, "The homepage has no title tag.")
	case containsName(home.Title, businessName):
		return pass(CheckTitleNamesBusiness, "The homepage title names the business: "+home.Title)
	default:
		return fail(CheckTitleNamesBusiness, fmt.Sprintf("The homepage title %q does not contain %q.", home.Title, businessName))
	}
}

// jsonLDFacts is the flattened view of every JSON-LD block on the site: which
// schema.org types were declared and which identity fields were populated. The
// blocks are walked generically rather than unmarshalled into a schema struct,
// because real sites nest them inside @graph, arrays, and vendor wrappers.
type jsonLDFacts struct {
	Parsed                                 bool
	Types                                  []string
	BusinessType                           string
	HasTelephone, HasAddress, HasOpenHours bool
}

// businessTypes are the schema.org types that identify an organisation. A
// specialised type (Dentist, MedicalClinic) sits under LocalBusiness, so the
// suffix match keeps the list short without missing the vertical forms.
var businessTypes = []string{"organization", "localbusiness", "medicalbusiness", "medicalclinic", "medicalorganization", "dentist", "physician", "hospital", "professionalservice", "healthandbeautybusiness"}

func collectJSONLD(pages []PageFacts) jsonLDFacts {
	facts := jsonLDFacts{}
	seen := map[string]bool{}
	for _, page := range pages {
		for _, block := range page.JSONLD {
			var parsed any
			if err := json.Unmarshal([]byte(block), &parsed); err != nil {
				continue
			}
			facts.Parsed = true
			walkJSONLD(parsed, &facts, seen)
		}
	}
	sort.Strings(facts.Types)
	return facts
}

// walkJSONLD descends through objects and arrays, recording every @type it
// meets and whether the identity fields appear anywhere in the graph.
func walkJSONLD(node any, facts *jsonLDFacts, seen map[string]bool) {
	switch value := node.(type) {
	case []any:
		for _, item := range value {
			walkJSONLD(item, facts, seen)
		}
	case map[string]any:
		for key, item := range value {
			switch strings.ToLower(key) {
			case "@type":
				for _, name := range jsonLDStrings(item) {
					if !seen[name] {
						seen[name] = true
						facts.Types = append(facts.Types, name)
					}
					lower := strings.ToLower(name)
					for _, candidate := range businessTypes {
						if lower == candidate || strings.HasSuffix(lower, candidate) {
							if facts.BusinessType == "" {
								facts.BusinessType = name
							}
						}
					}
				}
			case "telephone":
				facts.HasTelephone = facts.HasTelephone || jsonLDPopulated(item)
			case "address":
				facts.HasAddress = facts.HasAddress || jsonLDPopulated(item)
			case "openinghoursspecification", "openinghours":
				facts.HasOpenHours = facts.HasOpenHours || jsonLDPopulated(item)
			}
			walkJSONLD(item, facts, seen)
		}
	}
}

func jsonLDStrings(node any) []string {
	switch value := node.(type) {
	case string:
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return []string{trimmed}
		}
	case []any:
		var out []string
		for _, item := range value {
			out = append(out, jsonLDStrings(item)...)
		}
		return out
	}
	return nil
}

// jsonLDPopulated rejects a field that is present but empty, which is common in
// template-generated markup and would otherwise read as a pass.
func jsonLDPopulated(node any) bool {
	switch value := node.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []any:
		for _, item := range value {
			if jsonLDPopulated(item) {
				return true
			}
		}
	case map[string]any:
		for key, item := range value {
			if strings.HasPrefix(key, "@") {
				continue
			}
			if jsonLDPopulated(item) {
				return true
			}
		}
	case float64, bool:
		return true
	}
	return false
}

func containsName(text, name string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(strings.TrimSpace(name)))
}
