package visibility

import (
	"context"
	"encoding/json"
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
	for _, assessor := range Assessors(map[string]RolloutMode{"search-access": RolloutActive}) {
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

func TestCompilerExcludesShadowAndRanksDirectBlockerFirst(t *testing.T) {
	base := func(practice, assessor, subject string, reach int) AssessmentDraft {
		return AssessmentDraft{PracticeKey: practice, CriteriaVersion: 1, AssessorKey: assessor, AssessorVersion: 1, SubjectKey: subject, Status: StatusNotMet, Confidence: .8, Explanation: "Verified unmet practice.", Reach: reach, Persistence: 2, EvidenceQuality: 2, Actionability: 2, Effort: 2, PayloadVersion: 1, Payload: json.RawMessage(`{}`)}
	}
	drafts := []AssessmentDraft{base(PracticeAuthority, "influential-source", "source.example", 20), base(PracticeSearchAccess, "search-access", "site.example", 1), base(PracticeTopicCoverage, "tracked-topic", "topic", 10)}
	compiled, err := Compile(drafts, map[string]RolloutMode{"search-access": RolloutActive, "influential-source": RolloutActive, "tracked-topic": RolloutShadow})
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 2 {
		t.Fatalf("compiled count = %d, want 2", len(compiled))
	}
	if compiled[0].Draft.PracticeKey != PracticeSearchAccess {
		t.Fatalf("first practice = %s, want blocker", compiled[0].Draft.PracticeKey)
	}
	for _, item := range compiled {
		if err := ValidatePresentation(item.Presentation); err != nil {
			t.Fatalf("invalid presentation: %v", err)
		}
	}
}

func TestUnknownPayloadVersionFailsClosed(t *testing.T) {
	assessor := searchAssessor(t)
	_, err := assessor.Assess(context.Background(), NewEvidenceView([]EvidenceArtifact{{CollectorKey: CollectorOwnedSite, PayloadVersion: 99, Payload: json.RawMessage(`{}`)}}), nil)
	if err == nil {
		t.Fatal("expected unsupported evidence payload version error")
	}
}
