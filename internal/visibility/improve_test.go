package visibility

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCatalogPracticesHaveCompleteImproveMetadata(t *testing.T) {
	seen := map[string]bool{}
	for _, practice := range Catalog() {
		if practice.Key == "" || practice.Section == "" || practice.Title == "" || practice.Description == "" || practice.Why == "" || practice.AssessorKey == "" {
			t.Errorf("practice %q has incomplete catalog copy or ownership", practice.Key)
		}
		if seen[practice.Key] {
			t.Errorf("duplicate practice key %q", practice.Key)
		}
		seen[practice.Key] = true
		if practice.Mode != ModeCheck && practice.Mode != ModeContinuous && practice.Mode != ModeTracked {
			t.Errorf("practice %q has invalid mode %q", practice.Key, practice.Mode)
		}
		if practice.SubjectScope != ScopeBusiness && practice.SubjectScope != ScopeDynamic {
			t.Errorf("practice %q has invalid subject scope %q", practice.Key, practice.SubjectScope)
		}
		if practice.Mode == ModeTracked && practice.RecommendsActions {
			t.Errorf("tracked practice %q recommends actions without an explicit product exception", practice.Key)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("catalog contains %d practices, want 3", len(seen))
	}
}

func TestStandingAndModeLabelsAreIndependentOfActions(t *testing.T) {
	check, _ := Practice(PracticeSearchAccess)
	continuous, _ := Practice(PracticeAuthority)
	tests := []struct {
		def      PracticeDefinition
		status   AssessmentStatus
		standing ChecklistStanding
		label    string
	}{
		{check, StatusMet, StandingGood, "In place"},
		{check, StatusUnknown, StandingCouldNotVerify, "could not verify"},
		{continuous, StatusMet, StandingGood, "In good standing"},
		{continuous, StatusPartial, StandingImprovable, "Room to improve"},
		{continuous, StatusNotMet, StandingNeedsAttention, "Room to improve"},
	}
	for _, tt := range tests {
		got := Standing(tt.def, tt.status)
		if got != tt.standing {
			t.Errorf("Standing(%s,%s)=%s, want %s", tt.def.Key, tt.status, got, tt.standing)
		}
		if label := ModeLabel(tt.def.Mode, got); label != tt.label {
			t.Errorf("ModeLabel(%s,%s)=%q, want %q", tt.def.Mode, got, label, tt.label)
		}
	}
}

func TestRecommendationEligibilityRequiresCatalogCapability(t *testing.T) {
	if !Eligible(AssessmentDraft{PracticeKey: PracticeSearchAccess, Status: StatusNotMet}) {
		t.Fatal("actionable check was not eligible")
	}
	if Eligible(AssessmentDraft{PracticeKey: PracticeSearchAccess, Status: StatusMet}) {
		t.Fatal("good check was eligible")
	}
	tracked := PracticeDefinition{Key: "signal.test", Mode: ModeTracked, SubjectScope: ScopeBusiness, RecommendsActions: false}
	catalog = append(catalog, tracked)
	t.Cleanup(func() { catalog = catalog[:len(catalog)-1] })
	if Eligible(AssessmentDraft{PracticeKey: tracked.Key, Status: StatusNotMet}) {
		t.Fatal("tracked signal without recommendation capability was eligible")
	}
}

func TestSearchAccessUsesStableBusinessSubject(t *testing.T) {
	payload, _ := json.Marshal(OwnedSiteScan{PayloadVersion: 1, Host: "changing.example", Failure: "business website is not configured"})
	drafts, err := assessSearchAccess(context.Background(), NewEvidenceView([]EvidenceArtifact{{CollectorKey: CollectorOwnedSite, PayloadVersion: 1, Payload: payload}}), nil, AssessorManifest{Key: "search-access", ModuleVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].SubjectKey != BusinessSubjectKey || drafts[0].Status != StatusUnknown {
		t.Fatalf("draft=%+v, want business/UNKNOWN", drafts)
	}
}
