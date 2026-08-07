package visibility

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func artifact(t *testing.T, key string, value any) EvidenceArtifact {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return EvidenceArtifact{CollectorKey: key, CollectorVersion: 1, PayloadVersion: 1, Payload: payload}
}

func searchAssessor(t *testing.T) PracticeAssessor {
	t.Helper()
	for _, assessor := range Assessors() {
		if assessor.Manifest().Key == "search-access" {
			return assessor
		}
	}
	t.Fatal("search assessor missing")
	return nil
}

func TestSearchAccessAssessmentStatesFailClosed(t *testing.T) {
	tests := []struct {
		name string
		scan OwnedSiteScan
		want AssessmentStatus
	}{
		{name: "met", scan: OwnedSiteScan{PayloadVersion: 1, Host: "example.com", Reachable: true}, want: StatusMet},
		{name: "blocked", scan: OwnedSiteScan{PayloadVersion: 1, Host: "example.com", Reachable: true, OAIBlocked: true}, want: StatusNotMet},
		{name: "inspection failed", scan: OwnedSiteScan{PayloadVersion: 1, Host: "example.com", Failure: "timeout"}, want: StatusUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assessor := searchAssessor(t)
			drafts, err := assessor.Assess(context.Background(), NewEvidenceView([]EvidenceArtifact{artifact(t, CollectorOwnedSite, tt.scan)}), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(drafts) != 1 || drafts[0].Status != tt.want {
				t.Fatalf("drafts = %+v, want %s", drafts, tt.want)
			}
			if err := ValidateDraft(drafts[0], assessor.Manifest()); err != nil {
				t.Fatalf("invalid draft: %v", err)
			}
		})
	}
}

func unmetDraft(practice, assessor, subject string, reach int) AssessmentDraft {
	return AssessmentDraft{PracticeKey: practice, CriteriaVersion: 1, AssessorKey: assessor, AssessorVersion: 1, SubjectKey: subject, Status: StatusNotMet, Explanation: "Verified unmet practice.", Reach: reach, Persistence: 2, EvidenceQuality: 2, Actionability: 2, Effort: 2, PayloadVersion: 1, Payload: json.RawMessage(`{}`)}
}

func TestCompilerRanksDirectBlockerFirstAndPresentsFromCatalog(t *testing.T) {
	drafts := []AssessmentDraft{unmetDraft(PracticeAuthority, "influential-source", "source.example", 20), unmetDraft(PracticeSearchAccess, "search-access", "site.example", 1), unmetDraft(PracticeTopicCoverage, "tracked-topic", "topic", 10)}
	compiled, err := Compile(drafts)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 3 {
		t.Fatalf("compiled count = %d, want 3", len(compiled))
	}
	if compiled[0].Draft.PracticeKey != PracticeSearchAccess {
		t.Fatalf("first practice = %s, want blocker", compiled[0].Draft.PracticeKey)
	}
	for _, item := range compiled {
		if err := ValidatePresentation(item.Presentation); err != nil {
			t.Fatalf("invalid presentation: %v", err)
		}
		definition, _ := Practice(item.Draft.PracticeKey)
		wantTitle := definition.Title
		if definition.SubjectInTitle {
			wantTitle += ": " + item.Draft.SubjectKey
		}
		if item.Presentation.Title != wantTitle {
			t.Fatalf("title = %q, want %q", item.Presentation.Title, wantTitle)
		}
		if !slices.Contains(blockItems(item.Presentation, BlockQuestionList), definition.Steps[0]) {
			t.Fatalf("%s steps not taken from the catalog: %+v", item.Draft.PracticeKey, item.Presentation.Blocks)
		}
	}
}

// TestRankFollowsCatalogDirectBlocker moves the direct-blocker flag onto a
// different practice: the ranker must follow catalog data, so reintroducing a
// hardcoded practice key fails here.
func TestRankFollowsCatalogDirectBlocker(t *testing.T) {
	swapped := Catalog()
	for i := range swapped {
		swapped[i].DirectBlocker = swapped[i].Key == PracticeTopicCoverage
	}
	original := catalog
	catalog = swapped
	t.Cleanup(func() { catalog = original })

	drafts := []AssessmentDraft{unmetDraft(PracticeSearchAccess, "search-access", "site.example", 20), unmetDraft(PracticeTopicCoverage, "tracked-topic", "topic", 1)}
	Rank(drafts)
	if drafts[0].PracticeKey != PracticeTopicCoverage {
		t.Fatalf("first practice = %s, want the practice the catalog marks as blocking", drafts[0].PracticeKey)
	}
}

func blockItems(p Presentation, t BlockType) []string {
	for _, b := range p.Blocks {
		if b.Type == t {
			return b.Items
		}
	}
	return nil
}

func TestUnknownPayloadVersionFailsClosed(t *testing.T) {
	assessor := searchAssessor(t)
	_, err := assessor.Assess(context.Background(), NewEvidenceView([]EvidenceArtifact{{CollectorKey: CollectorOwnedSite, PayloadVersion: 99, Payload: json.RawMessage(`{}`)}}), nil)
	if err == nil {
		t.Fatal("expected unsupported evidence payload version error")
	}
}
