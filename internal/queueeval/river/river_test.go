package river

import (
	"context"
	"sync"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/queueeval/queueevaltest"
	"opensight/internal/store"
	"opensight/internal/workflows"

	riverqueue "github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"
)

type recordingInserter struct {
	mu   sync.Mutex
	args []riverqueue.JobArgs
}

func (r *recordingInserter) Insert(_ context.Context, args riverqueue.JobArgs, _ *riverqueue.InsertOpts) (*rivertype.JobInsertResult, error) {
	r.mu.Lock()
	r.args = append(r.args, args)
	r.mu.Unlock()
	return &rivertype.JobInsertResult{}, nil
}

func TestCoarseJobsPreservePartialFailureAndStageIsolation(t *testing.T) {
	accountID, _ := domain.NewID()
	businessID, _ := domain.NewID()
	runID, _ := domain.NewID()
	promptA, _ := domain.NewID()
	promptB, _ := domain.NewID()
	resultA, _ := domain.NewID()
	resultB, _ := domain.NewID()

	ops := &queueevaltest.Operations{
		AccountID: accountID, BusinessID: businessID, RunID: runID,
		Prompts:   []workflows.PromptSnapshot{{ID: promptA}, {ID: promptB}},
		ResultIDs: []domain.ID{resultA, resultB}, FailPrompt: promptB, FailAnalysis: resultB,
		WorkDelay: 20 * time.Millisecond,
	}
	jobs := &recordingInserter{}
	monitor := &MonitorWorker{Ops: ops, Jobs: jobs, Concurrency: 2}
	err := monitor.Work(context.Background(), &riverqueue.Job[MonitorArgs]{
		JobRow: &rivertype.JobRow{ID: 42},
		Args: MonitorArgs{BusinessID: businessID, Platform: store.PlatformChatGPT,
			ScheduledFor: time.Now().UTC(), Trigger: store.RunTriggerScheduled},
	})
	require.NoError(t, err)
	require.True(t, ops.Finalized)
	require.Equal(t, 2, ops.MaxPromptConcurrency())
	require.Len(t, jobs.args, 1)

	analyzeArgs, ok := jobs.args[0].(AnalyzeArgs)
	require.True(t, ok)
	analyze := &AnalyzeWorker{Ops: ops, Jobs: jobs, Concurrency: 2}
	require.NoError(t, analyze.Work(context.Background(), &riverqueue.Job[AnalyzeArgs]{Args: analyzeArgs}))
	require.True(t, ops.Reconciled)
	require.Len(t, ops.ReconcileInputs, 1, "failed analysis must be excluded from reconcile")
	require.Len(t, jobs.args, 2)

	assessArgs, ok := jobs.args[1].(AssessArgs)
	require.True(t, ok)
	assess := &AssessWorker{Ops: ops}
	require.NoError(t, assess.Work(context.Background(), &riverqueue.Job[AssessArgs]{Args: assessArgs}))
	require.True(t, ops.Assessed)
}
