// Package visibility owns the site-audit check catalog and the finders that
// turn evidence into improvement findings. The two are deliberately separate:
// the audit is a closed, stable set of assertions about the customer's own
// site, while findings are an open set discovered from the monitoring corpus.
package visibility

import "strings"

// CheckOutcome is the verdict on one check. A check answers a single question
// the user can read and verify for themselves.
type CheckOutcome string

const (
	CheckPass           CheckOutcome = "PASS"
	CheckFail           CheckOutcome = "FAIL"
	CheckCouldNotVerify CheckOutcome = "COULD_NOT_VERIFY"
	CheckNotApplicable  CheckOutcome = "NOT_APPLICABLE"
	CheckNotAssessed    CheckOutcome = "NOT_ASSESSED"
)

// Check is catalog data: one assertion in everyday language. Title reads as
// the thing being true, so a passing row needs no rephrasing, and What supplies
// the fuller explanation shown only when the user expands the row.
type Check struct {
	Key, Group, Title, What string
	// Informational marks a check that reports a fact the user may legitimately
	// have chosen — blocking the training crawler, for one. It never counts
	// towards passed or failed and never produces a finding.
	Informational bool
	// Blocking marks a check whose failure stops the other checks from mattering
	// at all: if OpenAI cannot read the site, nothing else about it can help.
	Blocking bool
	// Fix is what to do when this check fails, and FixTitle names that work in
	// the imperative. A check with no Fix is reported on the checklist but never
	// becomes a finding — this is the per-check lever for deciding what is worth
	// a work item. Title states the thing being true and reads wrong on a
	// failure, which is why the imperative is written out rather than derived.
	FixTitle string
	Fix      []string
	// Priority orders findings from failed checks against each other, 1 (most
	// urgent) to 3. It never crosses the blocking boundary.
	Priority int
}

// CheckResult is the verdict against one catalog check. Detail carries the
// evidence in specifics ("2 of 7 pages carry noindex: /fees, /thank-you"),
// because the specifics are why the checklist shows checks at all.
type CheckResult struct {
	Key     string       `json:"key"`
	Outcome CheckOutcome `json:"outcome"`
	Detail  string       `json:"detail"`
	Sources []string     `json:"sources,omitempty"`
}

// Group keys. Groups order the checklist and carry the "why this matters"
// copy; they hold no status of their own.
const (
	GroupAccess    = "access"
	GroupStructure = "structure"
	GroupIdentity  = "identity"
)

type GroupDefinition struct {
	Key, Title, Description string
	References              []string
}

var groups = []GroupDefinition{
	{
		Key:         GroupAccess,
		Title:       "ChatGPT can open your website",
		Description: "We check whether ChatGPT's web tools can visit your public pages and are allowed to use them.",
		References:  []string{"https://platform.openai.com/docs/bots"},
	},
	{
		Key:         GroupStructure,
		Title:       "Your pages are easy to find and identify",
		Description: "We check whether automated tools can discover your pages, know which address is preferred, and tell one page from another.",
	},
	{
		Key:         GroupIdentity,
		Title:       "Software can read your business details",
		Description: "We check whether your site labels details like your phone number, address, and hours in a format automated tools can reliably understand.",
		References:  []string{"https://schema.org/LocalBusiness"},
	},
}

func Groups() []GroupDefinition { return append([]GroupDefinition(nil), groups...) }

// CategoryListings is the one category no check produces: work done on somebody
// else's website rather than the customer's own.
const CategoryListings = "listings"

// CategoryDefinition names a kind of change, which is how the work queue is
// filtered. The three site categories are the checklist groups themselves — a
// finding from a failed check belongs to that check's group — so there is one
// taxonomy, not two. The labels differ from the group titles because a group
// heads a list of assertions ("ChatGPT can open your website") while a category
// names a body of work ("Website access").
type CategoryDefinition struct{ Key, Label string }

var categories = []CategoryDefinition{
	{Key: GroupAccess, Label: "Website access"},
	{Key: GroupStructure, Label: "Site structure"},
	{Key: GroupIdentity, Label: "Business details"},
	{Key: CategoryListings, Label: "Listings & directories"},
}

func Categories() []CategoryDefinition {
	return append([]CategoryDefinition(nil), categories...)
}

// CategoryKeys is the catalog order, and it is also the work queue's order:
// changes to the customer's own site come before getting listed on somebody
// else's, because a site change is entirely within their control while a
// listing depends on a third party accepting it. Ordering the queue by how
// hard the work is to actually finish is the point of the sequence, so keep
// the slice ordered easiest-to-act-on first.
func CategoryKeys() []string {
	out := make([]string, 0, len(categories))
	for _, category := range categories {
		out = append(out, category.Key)
	}
	return out
}

// CategoryOrder is a category's position in that sequence. An unknown category
// sorts last rather than first, so a finder added without a catalog entry cannot
// silently take over the top of the queue.
func CategoryOrder(key string) int {
	for i, category := range categories {
		if category.Key == key {
			return i
		}
	}
	return len(categories)
}

func CategoryLabel(key string) string {
	for _, category := range categories {
		if category.Key == key {
			return category.Label
		}
	}
	return ""
}

// Check keys are constants because the catalog declares them and the audit
// emits them; a literal in one place and a typo in the other would only surface
// as a runtime mismatch.
const (
	CheckSiteReachable      = "site_reachable"
	CheckNoLoginWall        = "no_login_wall"
	CheckPagesAllowIndexing = "pages_allow_indexing"
	CheckRobotsOAISearchBot = "robots_allows_oai_searchbot"
	CheckRobotsChatGPTUser  = "robots_allows_chatgpt_user"
	CheckRobotsGPTBot       = "robots_gptbot"

	CheckSitemapPublished    = "sitemap_published"
	CheckRobotsSitemap       = "robots_declares_sitemap"
	CheckCanonicalURL        = "canonical_url_set"
	CheckTitlesUnique        = "titles_unique"
	CheckTitleNamesBusiness  = "homepage_title_names_business"
	CheckMetaDescription     = "meta_description_present"
	CheckStructuredData      = "structured_data_present"
	CheckStructuredType      = "structured_business_type"
	CheckStructuredTelephone = "structured_telephone"
	CheckStructuredAddress   = "structured_address"
	CheckStructuredOpenHours = "structured_opening_hours"
)

// Crawler user agents the robots checks report on, lowercased for matching.
const (
	AgentOAISearchBot = "oai-searchbot"
	AgentChatGPTUser  = "chatgpt-user"
	AgentGPTBot       = "gptbot"
)

var catalog = []Check{
	{
		Key: CheckSiteReachable, Group: GroupAccess, Blocking: true, Priority: 1,
		Title:    "Your website is available",
		What:     "We open your homepage and a small set of important pages linked from your site.",
		FixTitle: "Make your website reachable",
		Fix: []string{
			"Confirm the site loads for a visitor who is not signed in and has never visited before.",
			"Check with your host that the server answers requests from outside your own network.",
			"Once it responds, the next weekly check will confirm it.",
		},
	},
	{
		Key: CheckNoLoginWall, Group: GroupAccess, Blocking: true, Priority: 1,
		Title:    "Visitors can view public pages without signing in",
		What:     "We check whether your public pages show their content without asking a visitor to sign in.",
		FixTitle: "Remove the sign-in wall from public pages",
		Fix: []string{
			"Publish the affected pages without authentication.",
			"If the site is behind a staging password, remove it for the pages you want recommended.",
		},
	},
	{
		Key: CheckPagesAllowIndexing, Group: GroupAccess, Blocking: true, Priority: 1,
		Title:    "Your pages are allowed to appear in search results",
		What:     "Pages can carry a hidden noindex setting that tells search tools not to include them. We check each page for that setting.",
		FixTitle: "Remove the noindex directive blocking your pages",
		Fix: []string{
			"Remove the noindex directive from the pages named above.",
			"In most site builders this is a per-page \"hide from search engines\" toggle.",
			"Leave it in place only on pages you genuinely do not want found, such as a thank-you page.",
		},
	},
	{
		Key: CheckRobotsOAISearchBot, Group: GroupAccess, Blocking: true, Priority: 1,
		Title:    "ChatGPT Search is allowed to read your pages",
		What:     "We check your robots.txt instructions for OAI-SearchBot, the crawler used by ChatGPT Search.",
		FixTitle: "Allow OAI-SearchBot in robots.txt",
		Fix: []string{
			"Add an explicit allow for the crawler behind ChatGPT search to your robots.txt:",
			"User-agent: OAI-SearchBot",
			"Allow: /",
		},
	},
	{
		Key: CheckRobotsChatGPTUser, Group: GroupAccess, Blocking: true, Priority: 1,
		Title:    "ChatGPT can open pages when a user asks",
		What:     "We check your robots.txt instructions for ChatGPT-User, which opens a page on a user's behalf.",
		FixTitle: "Allow ChatGPT-User in robots.txt",
		Fix: []string{
			"Add an explicit allow for the agent that opens your page on a user's behalf:",
			"User-agent: ChatGPT-User",
			"Allow: /",
		},
	},
	{
		Key: CheckRobotsGPTBot, Group: GroupAccess, Informational: true, Priority: 3,
		Title: "OpenAI model-training access",
		What:  "We report whether your robots.txt instructions allow GPTBot, OpenAI's model-training crawler. Allowing it is your choice and does not affect your checklist result.",
	},

	{
		Key: CheckSitemapPublished, Group: GroupStructure, Priority: 2,
		Title:    "Your site provides a page list",
		What:     "A sitemap is a file that lists the pages on your website. We look for one at /sitemap.xml.",
		FixTitle: "Publish a sitemap",
		Fix: []string{
			"Publish a sitemap at /sitemap.xml listing your public pages.",
			"Most site builders generate one automatically once the setting is enabled.",
		},
	},
	{
		Key: CheckRobotsSitemap, Group: GroupStructure, Priority: 2,
		Title:    "Your crawler instructions link to the page list",
		What:     "Your robots.txt file gives automated tools instructions about your site. We check whether it also tells them where to find your sitemap.",
		FixTitle: "Point robots.txt at your sitemap",
		Fix: []string{
			"Add a line to your robots.txt naming the sitemap's full address:",
			"Sitemap: https://yourdomain.com/sitemap.xml",
		},
	},
	{
		Key: CheckCanonicalURL, Group: GroupStructure, Priority: 3,
		Title:    "Your preferred homepage address is clear",
		What:     "The same page can sometimes appear at several web addresses. We check whether your homepage identifies the one address that should be treated as the main version.",
		FixTitle: "Declare a canonical URL on the homepage",
		Fix: []string{
			"Set the homepage's canonical URL to its preferred address, so the same page reached by several addresses is counted once.",
		},
	},
	{
		Key: CheckTitlesUnique, Group: GroupStructure, Priority: 3,
		Title:    "Each page has its own title",
		What:     "A page title appears in the browser tab and helps automated tools understand what that page is about. We compare the titles of the pages we read.",
		FixTitle: "Give each page its own title",
		Fix: []string{
			"Give each page a title describing that page specifically, rather than repeating the business name alone.",
		},
	},
	{
		Key: CheckTitleNamesBusiness, Group: GroupStructure, Priority: 2,
		Title:    "Your business name appears in the homepage title",
		What:     "We compare the title shown in the homepage's browser tab with your confirmed business name.",
		FixTitle: "Put your business name in the homepage title",
		Fix: []string{
			"Put your business name in the homepage title, spelled exactly as you want it recommended.",
		},
	},
	{
		Key: CheckMetaDescription, Group: GroupStructure, Priority: 3,
		Title:    "Your homepage includes a short summary",
		What:     "Websites can provide a hidden one-sentence summary for search and answer tools. We check whether your homepage includes one.",
		FixTitle: "Add a homepage meta description",
		Fix: []string{
			"Add a one-sentence meta description to the homepage naming what you do and where you are.",
		},
	},

	{
		Key: CheckStructuredData, Group: GroupIdentity, Priority: 2,
		Title:    "Your business information is labelled for software",
		What:     "Structured data is a hidden, standard format that labels business facts for software. We check the pages we read for this information.",
		FixTitle: "Publish structured data about your business",
		Fix: []string{
			"Add a schema.org JSON-LD block to your homepage describing the business.",
			"Most site builders have a plugin or built-in setting for this.",
		},
	},
	{
		Key: CheckStructuredType, Group: GroupIdentity, Priority: 2,
		Title:    "Your type of business is clearly identified",
		What:     "We check whether your structured data identifies what kind of business you are, such as a dental clinic or medical clinic.",
		FixTitle: "Declare your business type in structured data",
		Fix: []string{
			"Set the structured data's @type to the most specific type that fits, such as Dentist or MedicalClinic.",
		},
	},
	{
		Key: CheckStructuredTelephone, Group: GroupIdentity, Priority: 2,
		Title:    "Software can recognise your phone number",
		What:     "We check whether your phone number is labelled in structured data or linked as a number that can be called.",
		FixTitle: "Make your phone number machine-readable",
		Fix: []string{
			"Add a telephone field to your structured data, or link the number on the page as a tel: link.",
			"Use the same number and format as your other listings.",
		},
	},
	{
		Key: CheckStructuredAddress, Group: GroupIdentity, Priority: 2,
		Title:    "Software can recognise your address",
		What:     "We check whether your postal address is labelled in your site's structured data.",
		FixTitle: "Make your address machine-readable",
		Fix: []string{
			"Add a postal address to your structured data, matching the address on your other listings exactly.",
		},
	},
	{
		Key: CheckStructuredOpenHours, Group: GroupIdentity, Priority: 2,
		Title:    "Software can recognise your opening hours",
		What:     "We check whether your opening hours are labelled in your site's structured data.",
		FixTitle: "Make your opening hours machine-readable",
		Fix: []string{
			"Add opening hours to your structured data. Answers often cite availability as a reason to recommend one clinic over another.",
		},
	},
}

func Catalog() []Check { return append([]Check(nil), catalog...) }

func CheckByKey(key string) (Check, bool) {
	for _, c := range catalog {
		if c.Key == key {
			return c, true
		}
	}
	return Check{}, false
}

// CheckOutcomeLabel is the single source of the words a user reads for an
// outcome. An informational check reports a choice, so its outcomes are worded
// neutrally rather than as a shortfall.
func CheckOutcomeLabel(outcome CheckOutcome, informational bool) string {
	if informational {
		switch outcome {
		case CheckPass, CheckNotApplicable:
			return "Noted"
		}
	}
	switch outcome {
	case CheckPass:
		return "Passed"
	case CheckFail:
		return "Needs attention"
	case CheckCouldNotVerify:
		return "Could not verify"
	case CheckNotApplicable:
		return "Not applicable"
	default:
		return "Not assessed"
	}
}

// GroupTitle is used where only the key is at hand, such as composing a finding
// title from a failed check.
func GroupTitle(key string) string {
	for _, g := range groups {
		if g.Key == key {
			return g.Title
		}
	}
	return strings.ToUpper(key)
}
