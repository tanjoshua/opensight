package jobs

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river/rivertype"
)

func TestWeeklySlotsRespectBoundariesAndActivation(t *testing.T) {
	id := uuid.MustParse("01950000-0000-7000-8000-0000000000d2")
	day := scheduleDay(id)
	base := time.Date(2026, 8, 9+day, weeklyRunHourUTC, 0, 0, 0, time.UTC)

	if got, ok := LatestDueWeeklySlot(id, base.Add(-time.Minute), base.Add(-time.Nanosecond)); ok || !got.Before(base) {
		t.Fatalf("slot before boundary = %v, ok=%v", got, ok)
	}
	if got, ok := LatestDueWeeklySlot(id, base, base); !ok || !got.Equal(base) {
		t.Fatalf("slot at boundary = %v, ok=%v", got, ok)
	}
	if _, ok := LatestDueWeeklySlot(id, base.Add(time.Second), base.Add(8*24*time.Hour)); !ok {
		t.Fatal("next week's latest slot should be due after activation")
	}
}

func TestAnalysisAndAssessmentCanRunAgainAfterCompletion(t *testing.T) {
	for name, states := range map[string][]rivertype.JobState{
		"analysis":   (AnalyzeArgs{}).InsertOpts().UniqueOpts.ByState,
		"assessment": (AssessArgs{}).InsertOpts().UniqueOpts.ByState,
	} {
		if slices.Contains(states, rivertype.JobStateCompleted) {
			t.Errorf("%s uniqueness includes completed jobs; manual reanalysis would not enqueue", name)
		}
	}
}

func TestLatestDueWeeklySlotCatchesUpOnlyLatest(t *testing.T) {
	id := uuid.MustParse("01950000-0000-7000-8000-0000000000d2")
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	slot, ok := LatestDueWeeklySlot(id, now.AddDate(0, -2, 0), now)
	if !ok {
		t.Fatal("expected due slot")
	}
	if now.Sub(slot) < 0 || now.Sub(slot) >= 7*24*time.Hour {
		t.Fatalf("latest slot age = %v", now.Sub(slot))
	}
	if next := NextWeeklySlot(id, now); !next.After(now) || next.Sub(slot) != 7*24*time.Hour {
		t.Fatalf("next slot = %v, latest = %v", next, slot)
	}
}
