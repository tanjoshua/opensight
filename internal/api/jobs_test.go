package api

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	river "github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type fakeJobClient struct {
	mu        sync.Mutex
	next      int64
	inserted  []river.JobArgs
	cancelled []int64
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
