package visibility

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// promotionThreshold is the share of hand-labelled fixture cases an assessor
// must get right before it earns a place in registeredAssessors. It is written
// down in design 09 so "good enough to register" is a number rather than a
// judgement call.
const promotionThreshold = 0.90

// minFixtureCases stops a corpus from quietly shrinking to a size where its
// score no longer means anything.
const minFixtureCases = 15

// statusNone labels a case where the correct behaviour is to produce no
// assessment for the subject at all — the recurrence gates, and subjects that
// were never this practice's business in the first place.
const statusNone = "NONE"

// fixtureCase pairs collector payloads with the verdict a human judged correct
// for one practice and subject, plus the reason that verdict is right. The
// payloads are held raw because they are exactly the bytes evidence_artifacts
// stores: a fixture is a real collector payload, not a restatement of one.
type fixtureCase struct {
	Name       string            `json:"name"`
	Note       string            `json:"note"`
	Practice   string            `json:"practice"`
	Subject    string            `json:"subject"`
	Expect     string            `json:"expect"`
	Monitoring json.RawMessage   `json:"monitoring"`
	Site       json.RawMessage   `json:"site"`
	Pages      map[string]string `json:"pages"`
}

// fixtureResearcher serves only the pages a fixture declares, so scoring never
// touches the network. It honours the manifest's URL budget exactly as the real
// bounded researcher does, which lets a fixture show what that budget costs.
type fixtureResearcher struct {
	pages     map[string]string
	remaining int
}

func (r *fixtureResearcher) Inspect(_ context.Context, in ResearchRequest) (ResearchResult, error) {
	if r.remaining <= 0 {
		return ResearchResult{}, errors.New("research URL budget exhausted")
	}
	r.remaining--
	text, ok := r.pages[in.URL]
	if !ok {
		return ResearchResult{}, fmt.Errorf("fixture source %s is unreachable", in.URL)
	}
	return ResearchResult{URL: in.URL, Text: text, CheckedAt: time.Unix(0, 0).UTC()}, nil
}

// TestAssessorQualityAgainstFixtures scores every implemented assessor against
// its hand-labelled corpus and is the gate that decides promotion.
//
// A registered assessor ships, so every wrong answer fails the build. An
// unregistered one is known to be below par: its score is reported and its
// misses are logged, because the number is the artifact we want and a red build
// would only invite someone to soften the labels. Adding an assessor to
// registeredAssessors flips it from reported to enforced — that one edit is the
// whole promotion.
//
// The corpora are deliberately weighted towards cases the heuristics are known
// to get wrong, so a score here is a stress score, not an expected field
// accuracy. Run with `go test -v ./internal/visibility/ -run Quality` to read
// the per-assessor scores and the individual misses.
func TestAssessorQualityAgainstFixtures(t *testing.T) {
	for _, assessor := range assessors {
		manifest := assessor.manifest
		t.Run(manifest.Key, func(t *testing.T) {
			cases := loadFixtures(t, manifest.Key)
			if len(cases) < minFixtureCases {
				t.Fatalf("%s has %d fixture cases, want at least %d", manifest.Key, len(cases), minFixtureCases)
			}
			hits := 0
			for _, c := range cases {
				got := replayFixture(t, assessor, c)
				if got == c.Expect {
					hits++
					continue
				}
				miss := fmt.Sprintf("%s: want %s, got %s — %s", c.Name, c.Expect, got, c.Note)
				if registered(manifest.Key) {
					t.Error(miss)
				} else {
					t.Log(miss)
				}
			}
			score := float64(hits) / float64(len(cases))
			t.Logf("assessor=%s registered=%t score=%d/%d (%.0f%%) promotion_threshold=%.0f%%",
				manifest.Key, registered(manifest.Key), hits, len(cases), score*100, promotionThreshold*100)
			if !registered(manifest.Key) && score >= promotionThreshold {
				t.Logf("%s now clears the promotion threshold: add it to registeredAssessors to start assessing and enforcing", manifest.Key)
			}
		})
	}
}

// replayFixture runs one assessor over one fixture's stored payloads and
// returns the status it produced for the labelled subject, or statusNone when
// it produced no assessment for that subject.
func replayFixture(t *testing.T, assessor staticAssessor, c fixtureCase) string {
	t.Helper()
	manifest := assessor.manifest
	artifacts := []EvidenceArtifact{}
	if len(c.Monitoring) > 0 {
		artifacts = append(artifacts, EvidenceArtifact{CollectorKey: CollectorMonitoring, CollectorVersion: 1, PayloadVersion: 1, Payload: c.Monitoring})
	}
	if len(c.Site) > 0 {
		artifacts = append(artifacts, EvidenceArtifact{CollectorKey: CollectorOwnedSite, CollectorVersion: 1, PayloadVersion: 1, Payload: c.Site})
	}
	research := &fixtureResearcher{pages: c.Pages, remaining: manifest.MaxURLInspections}
	drafts, err := assessor.Assess(context.Background(), NewEvidenceView(artifacts), research)
	if err != nil {
		return "error: " + err.Error()
	}
	for _, d := range drafts {
		if d.PracticeKey == c.Practice && d.SubjectKey == c.Subject {
			return string(d.Status)
		}
	}
	return statusNone
}

func loadFixtures(t *testing.T, assessorKey string) []fixtureCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", assessorKey+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixtureCase
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode %s fixtures: %v", assessorKey, err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Note == "" || c.Practice == "" {
			t.Fatalf("%s fixture %q is missing a name, note or practice", assessorKey, c.Name)
		}
		switch AssessmentStatus(c.Expect) {
		case StatusMet, StatusPartial, StatusNotMet, StatusUnknown, StatusNotApplicable:
		default:
			if c.Expect != statusNone {
				t.Fatalf("%s fixture %q expects unknown status %q", assessorKey, c.Name, c.Expect)
			}
		}
		if seen[c.Name] {
			t.Fatalf("%s has two fixtures named %q", assessorKey, c.Name)
		}
		seen[c.Name] = true
		// Assessors decode payloads leniently, so a mistyped key would read as a
		// zero value and silently change a fixture's verdict rather than fail.
		// Fixtures are checked strictly instead: a key the payload struct does
		// not name is a broken fixture, not a false one.
		strictDecode[MonitoringSnapshot](t, assessorKey, c.Name, CollectorMonitoring, c.Monitoring)
		strictDecode[OwnedSiteScan](t, assessorKey, c.Name, CollectorOwnedSite, c.Site)
	}
	return cases
}

func strictDecode[T any](t *testing.T, assessorKey, name, collector string, raw json.RawMessage) {
	t.Helper()
	if len(raw) == 0 {
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var out T
	if err := decoder.Decode(&out); err != nil {
		t.Fatalf("%s fixture %q has an invalid %s payload: %v", assessorKey, name, collector, err)
	}
}
