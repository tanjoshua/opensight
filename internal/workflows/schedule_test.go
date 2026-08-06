package workflows

import (
	"testing"

	"opensight/internal/billing"
	"opensight/internal/store"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

func TestScheduleIDFormat(t *testing.T) {
	id := uuid.MustParse("01950000-0000-7000-8000-0000000000d2")
	got := ScheduleID(id, store.PlatformChatGPT)
	want := "monitor-01950000-0000-7000-8000-0000000000d2-chatgpt"
	if got != want {
		t.Fatalf("ScheduleID = %q, want %q", got, want)
	}
}

// TestScheduleSpecForWeekly checks the weekly spec fires once a week on the
// jittered day at the fixed hour, and that the day is stable and in range.
func TestScheduleSpecForWeekly(t *testing.T) {
	id := uuid.MustParse("01950000-0000-7000-8000-0000000000d2")

	spec, err := ScheduleSpecFor(id, RunIntervalWeekly)
	if err != nil {
		t.Fatalf("ScheduleSpecFor: %v", err)
	}
	if len(spec.Calendars) != 1 {
		t.Fatalf("calendars = %d, want 1", len(spec.Calendars))
	}
	cal := spec.Calendars[0]
	if len(cal.DayOfWeek) != 1 {
		t.Fatalf("day-of-week ranges = %d, want 1", len(cal.DayOfWeek))
	}
	day := cal.DayOfWeek[0].Start
	if day < 0 || day > 6 {
		t.Fatalf("day-of-week = %d, want 0..6", day)
	}
	if cal.Hour[0].Start != scheduleRunHourUTC {
		t.Fatalf("hour = %d, want %d", cal.Hour[0].Start, scheduleRunHourUTC)
	}

	// Stable across calls for the same business.
	if again := scheduleDayOfWeek(id); again != day {
		t.Fatalf("day-of-week not stable: %d then %d", day, again)
	}
}

func TestScheduleSpecForUnknownInterval(t *testing.T) {
	if _, err := ScheduleSpecFor(uuid.New(), "daily"); err == nil {
		t.Fatal("expected error for unmodelled run_interval")
	}
}

// TestRunWorkflowDerivesScheduledForWhenUnset proves a scheduled fire (which
// carries no explicit ScheduledFor in its static Args) still gets a non-zero
// date, so weekly runs land on distinct scheduled_for weeks instead of colliding.
func TestRunWorkflowDerivesScheduledForWhenUnset(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var a *Activities

	env.OnActivity(a.CheckRunAccess, mock.Anything, mock.Anything).
		Return(CheckRunAccessOutput{Access: billing.AccessFull.String()}, nil).Once()
	env.OnActivity(a.LoadRunSpec, mock.Anything, mock.MatchedBy(func(in LoadRunSpecInput) bool {
		return !in.ScheduledFor.IsZero()
	})).Return(specWithPrompts(t, 0), nil).Once()
	env.OnActivity(a.FinalizeRun, mock.Anything, mock.Anything).
		Return(store.Run{Status: store.RunStatusFailed}, nil).Once()
	env.OnWorkflow(AnalyzeRun, mock.Anything, mock.Anything).Return(nil).Once()
	env.OnWorkflow(AssessmentWorkflow, mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(RunWorkflow, RunWorkflowInput{
		BusinessID: mustID(t),
		Platform:   store.PlatformChatGPT,
		Trigger:    store.RunTriggerScheduled,
		// ScheduledFor intentionally left zero, as a Schedule fire would.
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}
