package workflows

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"opensight/internal/domain"
	"opensight/internal/store"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// RunIntervalWeekly is the only plan run_interval modelled at MVP (design 02:
// Starter is weekly). New intervals are new plan rows plus a case below.
const RunIntervalWeekly = "weekly"

// scheduleRunHourUTC is the fixed hour (UTC) at which weekly runs fire. Per-
// business jitter (design 04) spreads the day of week, not the hour.
const scheduleRunHourUTC = 2

// ScheduleID is the deterministic Temporal Schedule id for a business's
// monitoring on a platform (design 04): monitor-{business_id}-chatgpt.
func ScheduleID(businessID domain.ID, platform string) string {
	return fmt.Sprintf("monitor-%s-%s", businessID, platform)
}

// scheduleDayOfWeek derives a stable per-business day of week (0=Sunday..6=Saturday)
// from the business id. Hashing spreads weekly runs across the week rather than
// firing every tenant on the same day, smoothing API rate-limit and cost spikes
// from day one (design 04 jitter rationale).
func scheduleDayOfWeek(businessID domain.ID) int {
	h := fnv.New32a()
	_, _ = h.Write(businessID[:])
	return int(h.Sum32() % 7)
}

// ScheduleSpecFor derives the Temporal ScheduleSpec from the plan's run_interval.
// An unknown interval is an error, never a silent default — entitlements come
// from the plan row (design 02), so an unmodelled interval is a real config gap.
func ScheduleSpecFor(businessID domain.ID, runInterval string) (client.ScheduleSpec, error) {
	switch runInterval {
	case RunIntervalWeekly:
		return client.ScheduleSpec{
			Calendars: []client.ScheduleCalendarSpec{{
				DayOfWeek: []client.ScheduleRange{{Start: scheduleDayOfWeek(businessID)}},
				Hour:      []client.ScheduleRange{{Start: scheduleRunHourUTC}},
				Minute:    []client.ScheduleRange{{Start: 0}},
			}},
			TimeZoneName: "UTC",
		}, nil
	default:
		return client.ScheduleSpec{}, fmt.Errorf("unsupported plan run_interval %q", runInterval)
	}
}

// CreateScheduleParams are the inputs for CreateMonitorSchedule.
type CreateScheduleParams struct {
	BusinessID  domain.ID
	Platform    string
	RunInterval string
	TaskQueue   string
}

// ScheduleCreator is the narrow seam CreateMonitorSchedule needs. client.Client
// satisfies it; so does any interface that exposes just ScheduleClient(), which
// keeps the API layer from having to depend on the full Temporal client surface.
type ScheduleCreator interface {
	ScheduleClient() client.ScheduleClient
}

// CreateMonitorSchedule creates the recurring monitoring Schedule for a business
// (design 04). The action starts RunWorkflow with trigger=scheduled and no
// explicit ScheduledFor: a Schedule fires with static Args, so each fire derives
// its own week bucket inside the workflow (see RunWorkflow). Overlap policy is
// Skip. Creating an already-existing schedule is treated as success so re-seeding
// is idempotent.
func CreateMonitorSchedule(ctx context.Context, c ScheduleCreator, params CreateScheduleParams) (string, error) {
	spec, err := ScheduleSpecFor(params.BusinessID, params.RunInterval)
	if err != nil {
		return "", err
	}

	scheduleID := ScheduleID(params.BusinessID, params.Platform)
	_, err = c.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:      scheduleID,
		Spec:    spec,
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		Action: &client.ScheduleWorkflowAction{
			// Temporal appends the nominal fire time to this id for uniqueness;
			// the DB UNIQUE(business, platform, scheduled_for) is the real
			// idempotency anchor (design 04), so the exact suffix is cosmetic.
			ID:        fmt.Sprintf("run-%s-%s", params.BusinessID, params.Platform),
			Workflow:  RunWorkflow,
			TaskQueue: params.TaskQueue,
			Args: []any{RunWorkflowInput{
				BusinessID: params.BusinessID,
				Platform:   params.Platform,
				Trigger:    store.RunTriggerScheduled,
			}},
		},
	})
	if err != nil {
		var alreadyExists *serviceerror.AlreadyExists
		if errors.As(err, &alreadyExists) {
			return scheduleID, nil
		}
		return "", fmt.Errorf("create schedule %s: %w", scheduleID, err)
	}
	return scheduleID, nil
}

// SetMonitorSchedulePaused pauses or resumes a business's monitoring
// Schedule on platform (design 08 "Schedule gate" — reconcile's gate 2).
// A schedule that does not exist yet is success, not failure,
// mirroring CreateMonitorSchedule's AlreadyExists swallow: a tenant that
// lapses before onboarding ever created a Schedule (or before an admin
// re-seeds one) is a normal state, not an error the caller should surface.
func SetMonitorSchedulePaused(ctx context.Context, c ScheduleCreator, businessID domain.ID, platform string, paused bool) error {
	scheduleID := ScheduleID(businessID, platform)
	handle := c.ScheduleClient().GetHandle(ctx, scheduleID)

	description, err := handle.Describe(ctx)
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("describe schedule %s: %w", scheduleID, err)
	}
	currentlyPaused := description.Schedule.State != nil && description.Schedule.State.Paused
	if currentlyPaused == paused {
		return nil
	}

	if paused {
		err = handle.Pause(ctx, client.SchedulePauseOptions{Note: "billing: access lapsed"})
	} else {
		err = handle.Unpause(ctx, client.ScheduleUnpauseOptions{Note: "billing: access restored"})
	}
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("set schedule %s paused=%t: %w", scheduleID, paused, err)
	}
	return nil
}
