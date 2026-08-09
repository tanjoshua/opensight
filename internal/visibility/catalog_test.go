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

// TestRankPutsBlockersThenOwnSiteWork pins the triage order the Actions page
// depends on: nothing outranks a blocker, work on the customer's own site comes
// before work that depends on a third party accepting a listing however many
// answers that listing would affect, and reach orders the listings against each
// other where the comparison is fair.
func TestRankPutsBlockersThenOwnSiteWork(t *testing.T) {
	findings := []Finding{
		{Key: "listed-wide", Category: CategoryListings, Reach: 9, Priority: 1},
		{Key: "details", Category: GroupIdentity, Priority: 2},
		{Key: "blocker", Category: GroupAccess, Blocking: true, Priority: 3},
		{Key: "listed-narrow", Category: CategoryListings, Reach: 2, Priority: 1},
		{Key: "structure", Category: GroupStructure, Priority: 3},
	}
	Rank(findings)
	got := make([]string, 0, len(findings))
	for _, f := range findings {
		got = append(got, f.Key)
	}
	want := []string{"blocker", "structure", "details", "listed-wide", "listed-narrow"}
	if !slices.Equal(got, want) {
		t.Errorf("got order %v, want %v", got, want)
	}
}
