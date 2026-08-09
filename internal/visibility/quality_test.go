package visibility

import (
	"bytes"
	"context"
	"encoding/json"

	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"opensight/internal/llm"
)

// minFixtureCases stops a corpus from quietly shrinking to a size where its
// score no longer means anything. Everything that ships is scored against a
// hand-labelled corpus, and every wrong answer fails the build — the corpora
// are deliberately weighted towards cases the logic is known to find hard, so
// a pass here is a stress result rather than an expected field accuracy.
const minFixtureCases = 15

// fixtureResearcher serves only the pages a fixture declares, so scoring never
// touches the network. It honours a URL budget exactly as the real bounded
// researcher does, which lets a fixture show what that budget costs.
type fixtureResearcher struct {
	pages     map[string]string
	remaining int
}

func (r *fixtureResearcher) Inspect(_ context.Context, in ResearchRequest) (ResearchResult, error) {
	if r.remaining <= 0 {
		return ResearchResult{}, ErrResearchBudget
	}
	r.remaining--
	text, ok := r.pages[in.URL]
	if !ok {
		return ResearchResult{}, fmt.Errorf("fixture source %s is unreachable", in.URL)
	}
	return ResearchResult{URL: in.URL, Text: text, CheckedAt: time.Unix(0, 0).UTC()}, nil
}

// auditCase pairs a real site-scan payload with the outcomes a human judged
// correct. Expect names only the checks that are not expected to pass, so a
// case that adds a new failure has to say so; every unnamed check must pass.
type auditCase struct {
	Name   string                  `json:"name"`
	Note   string                  `json:"note"`
	Expect map[string]CheckOutcome `json:"expect"`
	Site   json.RawMessage         `json:"site"`
}

// TestAuditAgainstFixtures scores the site audit against its corpus. The audit
// is deterministic, so any disagreement is a real behaviour change.
func TestAuditAgainstFixtures(t *testing.T) {
	cases := loadCases[auditCase](t, "audit")
	if len(cases) < minFixtureCases {
		t.Fatalf("audit has %d fixture cases, want at least %d", len(cases), minFixtureCases)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var scan OwnedSiteScan
			strictDecode(t, c.Name, "site", c.Site, &scan)
			got := map[string]CheckOutcome{}
			for _, result := range Audit(scan) {
				got[result.Key] = result.Outcome
			}
			for _, check := range catalog {
				want, named := c.Expect[check.Key]
				if !named {
					want = CheckPass
				}
				if got[check.Key] != want {
					t.Errorf("check %s: want %s, got %s — %s", check.Key, want, got[check.Key], c.Note)
				}
			}
			for key := range c.Expect {
				if _, ok := CheckByKey(key); !ok {
					t.Errorf("fixture expects unknown check %q", key)
				}
			}
		})
	}
}

// finderCase pairs collector payloads with the exact set of finding keys a
// human judged correct. The empty set is a real and common label: most of this
// corpus is cases where the honest answer is to recommend nothing.
type finderCase struct {
	Name       string            `json:"name"`
	Note       string            `json:"note"`
	Expect     []string          `json:"expect"`
	Monitoring json.RawMessage   `json:"monitoring"`
	Site       json.RawMessage   `json:"site"`
	Pages      map[string]string `json:"pages"`
}

// TestCitationGapAgainstFixtures scores the citation-gap finder. Its corpus is
// built around the ways a name match goes wrong — substrings, shortened forms,
// host variants, a rival's advert — because a false "get listed here" is the
// failure that costs the product its credibility.
func TestCitationGapAgainstFixtures(t *testing.T) {
	cases := loadCases[finderCase](t, "citation-gap")
	if len(cases) < minFixtureCases {
		t.Fatalf("citation-gap has %d fixture cases, want at least %d", len(cases), minFixtureCases)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			in := FinderInput{}
			strictDecode(t, c.Name, "monitoring", c.Monitoring, &in.Snapshot)
			var scan OwnedSiteScan
			strictDecode(t, c.Name, "site", c.Site, &scan)
			in.Audit = Audit(scan)
			in.Classifier = llm.NewStubSourceClassifier()

			research := &fixtureResearcher{pages: c.Pages, remaining: ResearchURLBudget}
			findings, err := citationGapFinder{}.Find(context.Background(), in, research)
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			got := make([]string, 0, len(findings))
			for _, f := range findings {
				got = append(got, f.Key)
				if f.Reach != len(f.ResultIDs) {
					t.Errorf("finding %s reach = %d, want %d affected answers", f.Key, f.Reach, len(f.ResultIDs))
				}
			}
			sort.Strings(got)
			want := append([]string(nil), c.Expect...)
			sort.Strings(want)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("want findings %v, got %v — %s", want, got, c.Note)
			}
		})
	}
}

func loadCases[T any](t *testing.T, name string) []T {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode %s fixtures: %v", name, err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		named, ok := any(c).(interface{ label() (string, string) })
		if !ok {
			continue
		}
		caseName, note := named.label()
		if caseName == "" || note == "" {
			t.Fatalf("%s has a fixture missing a name or note", name)
		}
		if seen[caseName] {
			t.Fatalf("%s has two fixtures named %q", name, caseName)
		}
		seen[caseName] = true
	}
	return cases
}

func (c auditCase) label() (string, string)  { return c.Name, c.Note }
func (c finderCase) label() (string, string) { return c.Name, c.Note }

// strictDecode rejects a key the payload struct does not name. Payloads are
// decoded leniently in production, so without this a mistyped fixture key would
// read as a zero value and silently change a case's verdict rather than fail.
func strictDecode(t *testing.T, name, field string, raw json.RawMessage, out any) {
	t.Helper()
	if len(raw) == 0 {
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		t.Fatalf("fixture %q has an invalid %s payload: %v", name, field, err)
	}
}
