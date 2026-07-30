package reconcile

import (
	"context"
	"testing"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/store"

	"github.com/google/uuid"
)

// fakeBusinessLister is a scripted businessLister.
type fakeBusinessLister struct {
	businesses []store.Business
	err        error
}

func (f *fakeBusinessLister) ListBusinesses(context.Context, domain.ID) ([]store.Business, error) {
	return f.businesses, f.err
}

// TestMonitoringSetNoBusinessIsNoOp covers the AC that the schedule gate
// tolerates a tenant with no business yet: Set returns no error, and — since
// the loop over businesses never runs — the schedules seam is never touched,
// so nil is a valid value for it here.
func TestMonitoringSetNoBusinessIsNoOp(t *testing.T) {
	m := NewMonitoring(&fakeBusinessLister{}, nil)

	if err := m.Set(context.Background(), uuid.New(), billing.Starter.Platforms, false); err != nil {
		t.Fatalf("Set with no business: %v", err)
	}
}

// TestMonitoringSetPropagatesListError covers the ordinary error path: a
// businesses.ListBusinesses failure surfaces rather than being swallowed
// alongside the "no business" case.
func TestMonitoringSetPropagatesListError(t *testing.T) {
	m := NewMonitoring(&fakeBusinessLister{err: errBoom}, nil)

	if err := m.Set(context.Background(), uuid.New(), billing.Starter.Platforms, false); err == nil {
		t.Fatal("Set with a list error: want error, got nil")
	}
}

type boomErr string

func (e boomErr) Error() string { return string(e) }

var errBoom = boomErr("boom")
