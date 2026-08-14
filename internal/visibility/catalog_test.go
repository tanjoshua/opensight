package visibility

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// TestAuditCoversEveryCatalogCheck is the drift guard between the two halves of
// the audit: the catalog declares what a user is shown, and Audit decides the
// outcomes. Either side alone compiles fine, so nothing but this catches a
// check that is declared and never evaluated, or evaluated and never declared.
func TestAuditCoversEveryCatalogCheck(t *testing.T) {
	scans := map[string]OwnedSiteScan{
		"fetch failure": {FetchFailure: "dial tcp: lookup failed"},
		"no page markup": {Reachable: true, CheckedURLs: []string{"https://example.com/"},
			AgentDenied: map[string]bool{AgentOAISearchBot: false, AgentChatGPTUser: false, AgentGPTBot: false}},
		"full scan": {Reachable: true, BusinessName: "Bright Smile Dental", RobotsFound: true, SitemapFound: true,
			AgentDenied: map[string]bool{AgentOAISearchBot: false, AgentChatGPTUser: false, AgentGPTBot: false},
			Pages:       []PageFacts{{URL: "https://example.com/", Title: "Bright Smile Dental", Canonical: "https://example.com/"}}},
	}
	for name, scan := range scans {
		t.Run(name, func(t *testing.T) {
			results := Audit(scan)
			if len(results) != len(catalog) {
				t.Fatalf("got %d results for %d catalog checks", len(results), len(catalog))
			}
			for i, result := range results {
				if result.Key != catalog[i].Key {
					t.Errorf("position %d: got check %q, want %q — results must be in catalog order", i, result.Key, catalog[i].Key)
				}
				if strings.TrimSpace(result.Detail) == "" {
					t.Errorf("check %q has no evidence detail", result.Key)
				}
				switch result.Outcome {
				case CheckPass, CheckFail, CheckCouldNotVerify, CheckNotApplicable:
				default:
					t.Errorf("check %q has invalid outcome %q", result.Key, result.Outcome)
				}
			}
		})
	}
}

// TestCatalogIsWellFormed guards the catalog edits that would otherwise reach a
// user as an empty finding title or a check with no group to render under.
func TestCatalogIsWellFormed(t *testing.T) {
	known := map[string]bool{}
	for _, g := range Groups() {
		known[g.Key] = true
		// A site-audit finding takes its category from the check's group, so a
		// group with no matching category would reach the work queue unlabelled
		// and unfilterable — visible only by looking at the page.
		if CategoryLabel(g.Key) == "" {
			t.Errorf("group %q has no matching category", g.Key)
		}
	}
	seen := map[string]bool{}
	for _, c := range catalog {
		switch {
		case seen[c.Key]:
			t.Errorf("duplicate check key %q", c.Key)
		case !known[c.Group]:
			t.Errorf("check %q names unknown group %q", c.Key, c.Group)
		case c.Title == "" || c.What == "":
			t.Errorf("check %q is missing a title or a description of what is tested", c.Key)
		case c.Priority < 1 || c.Priority > 3:
			t.Errorf("check %q has priority %d, want 1-3", c.Key, c.Priority)
		case len(c.Fix) > 0 && c.FixTitle == "":
			t.Errorf("check %q declares a fix but no imperative title to show on the finding", c.Key)
		case c.Informational && len(c.Fix) > 0:
			t.Errorf("check %q is informational, so it reports a choice and must not prescribe a fix", c.Key)
		case c.DependsOn == c.Key:
			t.Errorf("check %q depends on itself, which would fold it out of the queue entirely", c.Key)
		case c.DependsOn != "" && !seen[c.DependsOn]:
			// Requiring the parent earlier in the catalog keeps the graph a
			// forest rooted in checks the queue can actually reach, so no cycle
			// can silently swallow every finding in a chain.
			t.Errorf("check %q depends on %q, which must be declared before it", c.Key, c.DependsOn)
		}
		seen[c.Key] = true
	}
}

// TestSiteAuditFinderOnlyActsOnFixableFailures pins the per-check lever: a
// failing check without a fix, and an informational check whatever its outcome,
// belong on the checklist but never in the work queue.
func TestSiteAuditFinderOnlyActsOnFixableFailures(t *testing.T) {
	audit := []CheckResult{
		{Key: CheckRobotsOAISearchBot, Outcome: CheckFail, Detail: "blocked"},
		{Key: CheckRobotsGPTBot, Outcome: CheckFail, Detail: "blocked"},
		{Key: CheckStructuredOpenHours, Outcome: CheckFail, Detail: "no hours"},
		{Key: CheckSitemapPublished, Outcome: CheckCouldNotVerify, Detail: "unknown"},
		{Key: CheckMetaDescription, Outcome: CheckPass, Detail: "present"},
	}
	findings, err := siteAuditFinder{}.Find(context.Background(), FinderInput{Audit: audit}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Key != SourceSiteAudit+":"+CheckRobotsOAISearchBot {
		t.Fatalf("want only the fixable failure, got %+v", findings)
	}
	if !findings[0].Blocking || findings[0].Title == "" || len(findings[0].Steps) == 0 {
		t.Errorf("finding lost its blocking flag, title or steps: %+v", findings[0])
	}
	if findings[0].Category != GroupAccess {
		t.Errorf("got category %q, want the failed check's group %q", findings[0].Category, GroupAccess)
	}
}

// TestStructuredDataFailuresBecomeOneAction pins the consolidation and the
// snippet together, because they are the same promise: a site with no JSON-LD
// has one job to do, and the action states the exact block to publish rather
// than the category of thing to publish.
func TestStructuredDataFailuresBecomeOneAction(t *testing.T) {
	audit := []CheckResult{
		{Key: CheckStructuredData, Outcome: CheckFail, Detail: "No page we read publishes a readable application/ld+json block."},
		{Key: CheckStructuredType, Outcome: CheckFail, Detail: "no type"},
		{Key: CheckStructuredTelephone, Outcome: CheckFail, Detail: "no phone"},
		{Key: CheckStructuredAddress, Outcome: CheckFail, Detail: "no address"},
		{Key: CheckStructuredOpenHours, Outcome: CheckFail, Detail: "no hours"},
	}
	profile := SiteProfile{
		Name: "Roots Advanced Endodontics", Website: "https://rootsendo.sg",
		Address: "10 Sinaran Drive", City: "Singapore", Country: "SG",
	}
	findings, err := siteAuditFinder{}.Find(context.Background(), FinderInput{Audit: audit, Profile: profile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The telephone check survives on its own: a tel: link fixes it without any
	// structured data, so it is not entailed by the parent failure.
	keys := []string{}
	for _, f := range findings {
		keys = append(keys, f.Key)
	}
	want := []string{SourceSiteAudit + ":" + CheckStructuredData, SourceSiteAudit + ":" + CheckStructuredTelephone}
	if !slices.Equal(keys, want) {
		t.Fatalf("got actions %v, want %v", keys, want)
	}

	steps := strings.Join(findings[0].Steps, "\n")
	for _, fragment := range []string{`"@context"`, "Roots Advanced Endodontics", "https://rootsendo.sg", "10 Sinaran Drive", "application/ld+json"} {
		if !strings.Contains(steps, fragment) {
			t.Errorf("snippet is missing %q:\n%s", fragment, steps)
		}
	}
	// Opening hours stay on the checklist rather than being smuggled into this
	// broader action. The address is already in the generated block.
	if strings.Contains(steps, "openingHours") || strings.Contains(steps, "Add address") {
		t.Errorf("action asks for a checklist-only or already-filled field:\n%s", steps)
	}
	if !strings.Contains(findings[0].Detail, "2 dependent checks") {
		t.Errorf("detail should account for the folded checks: %q", findings[0].Detail)
	}
}

// TestDependentCheckStandsAloneWhenParentPasses is the other half of the fold:
// a site that publishes JSON-LD but omits one field has real, specific work to
// do, and folding is conditional on the parent actually having failed.
func TestDependentCheckStandsAloneWhenParentPasses(t *testing.T) {
	audit := []CheckResult{
		{Key: CheckStructuredData, Outcome: CheckPass, Detail: "Found structured data declaring Dentist."},
		{Key: CheckStructuredAddress, Outcome: CheckFail, Detail: "Structured data includes no postal address."},
	}
	findings, err := siteAuditFinder{}.Find(context.Background(), FinderInput{Audit: audit}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Key != SourceSiteAudit+":"+CheckStructuredAddress {
		t.Fatalf("want the address check as its own action, got %+v", findings)
	}
}

// TestStructuredDataActionInventsNothing guards the rule that makes the snippet
// safe to paste: with no confirmed profile there is no block to offer, and the
// action falls back to the catalog's generic fix rather than emitting a
// template full of placeholder values a user might publish unedited.
func TestStructuredDataActionInventsNothing(t *testing.T) {
	audit := []CheckResult{{Key: CheckStructuredData, Outcome: CheckFail, Detail: "none"}}
	findings, err := siteAuditFinder{}.Find(context.Background(), FinderInput{Audit: audit}, nil)
	if err != nil {
		t.Fatal(err)
	}
	check, _ := CheckByKey(CheckStructuredData)
	if len(findings) != 1 || !slices.Equal(findings[0].Steps, check.Fix) {
		t.Fatalf("want the catalog fix verbatim, got %+v", findings)
	}
}

// TestRankPutsBlockersThenOwnSiteWork pins the triage order the Actions page
// depends on: nothing outranks a blocker, work on the customer's own site comes
// before work that depends on a third party accepting a listing however many
// answers that listing would affect, and reach orders the listings against each
// other where the comparison is fair.
//
// The content entry pins the position that keeps reach from distorting the
// queue. A writing job carries per-answer reach and a markup fix cannot, so
// ranking them in one bucket would always bury the cheap certain fix under the
// expensive uncertain one; separate categories are what make that comparison
// never happen.
func TestRankPutsBlockersThenOwnSiteWork(t *testing.T) {
	findings := []Finding{
		{Key: "listed-wide", Category: CategoryListings, Reach: 9, Priority: 1},
		{Key: "details", Category: GroupIdentity, Priority: 2},
		{Key: "blocker", Category: GroupAccess, Blocking: true, Priority: 3},
		{Key: "listed-narrow", Category: CategoryListings, Reach: 2, Priority: 1},
		{Key: "structure", Category: GroupStructure, Priority: 3},
		{Key: "writing", Category: CategoryContent, Reach: 8, Priority: 1},
	}
	Rank(findings)
	got := make([]string, 0, len(findings))
	for _, f := range findings {
		got = append(got, f.Key)
	}
	want := []string{"blocker", "structure", "details", "writing", "listed-wide", "listed-narrow"}
	if !slices.Equal(got, want) {
		t.Errorf("got order %v, want %v", got, want)
	}
}
