package api

import (
	"context"
	"slices"
	"sync"
	"testing"

	"opensight/internal/domain"

	"github.com/jackc/pgx/v5"
	river "github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type fakeJobClient struct {
	mu        sync.Mutex
	next      int64
	inserted  []river.JobArgs
	cancelled []int64
	listed    []*rivertype.JobRow
}

func (f *fakeJobClient) add(args river.JobArgs) (*rivertype.JobInsertResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.inserted = append(f.inserted, args)
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: f.next}}, nil
}
func (f *fakeJobClient) Insert(_ context.Context, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return f.add(args)
}
func (f *fakeJobClient) InsertTx(_ context.Context, _ pgx.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return f.add(args)
}
func (f *fakeJobClient) JobCancel(_ context.Context, id int64) (*rivertype.JobRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	return &rivertype.JobRow{ID: id}, nil
}
func (f *fakeJobClient) JobList(_ context.Context, _ *river.JobListParams) (*river.JobListResult, error) {
	return &river.JobListResult{Jobs: f.listed}, nil
}

func TestMonitoringPending(t *testing.T) {
	businessID, err := domain.NewID()
	if err != nil {
		t.Fatalf("new business id: %v", err)
	}

	server := &Server{jobs: &fakeJobClient{listed: []*rivertype.JobRow{{ID: 41}}}}
	pending, err := server.monitoringPending(context.Background(), businessID)
	if err != nil {
		t.Fatalf("monitoringPending: %v", err)
	}
	if !pending {
		t.Fatal("monitoringPending = false, want true for a live job")
	}

	server.jobs = &fakeJobClient{}
	pending, err = server.monitoringPending(context.Background(), businessID)
	if err != nil {
		t.Fatalf("monitoringPending without jobs: %v", err)
	}
	if pending {
		t.Fatal("monitoringPending = true, want false without a live job")
	}

	server.jobs = nil
	pending, err = server.monitoringPending(context.Background(), businessID)
	if err != nil || pending {
		t.Fatalf("monitoringPending without a job client = %t, %v; want false, nil", pending, err)
	}
}

func TestFeaturePendingJobKinds(t *testing.T) {
	wantVisibility := []string{
		"opensight_monitor",
		"opensight_analyze",
	}
	if got := visibilityJobKinds(); !slices.Equal(got, wantVisibility) {
		t.Fatalf("visibility job kinds = %v, want %v", got, wantVisibility)
	}

	wantImprove := append(slices.Clone(wantVisibility), "opensight_assess")
	if got := improveJobKinds(); !slices.Equal(got, wantImprove) {
		t.Fatalf("improve job kinds = %v, want %v", got, wantImprove)
	}
}
