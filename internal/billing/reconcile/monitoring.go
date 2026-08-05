package reconcile

import (
	"context"
	"errors"
	"fmt"

	"opensight/internal/domain"
	"opensight/internal/store"
	"opensight/internal/workflows"
)

// businessLister is the one store method Monitoring needs. Returns an
// empty slice, not an error, for a account with no business yet (design 08:
// "tolerating a account with no business or no schedule yet").
type businessLister interface {
	ListBusinesses(ctx context.Context, accountID domain.ID) ([]store.Business, error)
}

// Monitoring implements reconcile's monitoringGate seam: pause or resume
// every one of a account's businesses' monitoring Schedules, across every
// platform the account's plan covers.
type Monitoring struct {
	businesses businessLister
	schedules  workflows.ScheduleCreator
}

// NewMonitoring builds a Monitoring gate.
func NewMonitoring(businesses businessLister, schedules workflows.ScheduleCreator) *Monitoring {
	return &Monitoring{businesses: businesses, schedules: schedules}
}

// Set pauses (enabled=false) or resumes (enabled=true) accountID's monitoring
// Schedules for every platform, for every business the account has. A account
// with no business is a no-op, not an error — onboarding may not have
// created one yet — and so is a business whose Schedule was never created
// (SetMonitorSchedulePaused swallows NotFound the same way
// CreateMonitorSchedule swallows AlreadyExists). Errors across
// businesses/platforms are joined rather than short-circuited, so one
// missing schedule doesn't stop the rest from being paused/resumed.
func (m *Monitoring) Set(ctx context.Context, accountID domain.ID, platforms []string, enabled bool) error {
	businesses, err := m.businesses.ListBusinesses(ctx, accountID)
	if err != nil {
		return fmt.Errorf("monitoring: list businesses: %w", err)
	}

	var errs []error
	for _, b := range businesses {
		for _, platform := range platforms {
			if err := workflows.SetMonitorSchedulePaused(ctx, m.schedules, b.ID, platform, !enabled); err != nil {
				errs = append(errs, fmt.Errorf("monitoring: set schedule for business %s platform %s: %w", b.ID, platform, err))
			}
		}
	}
	return errors.Join(errs...)
}
