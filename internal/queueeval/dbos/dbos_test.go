package dbos

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"opensight/internal/domain"
	"opensight/internal/queueeval/queueevaltest"
	"opensight/internal/store"
	"opensight/internal/workflows"

	dbosgo "github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/stretchr/testify/require"
)

func TestDurableWorkflowExecutesFanOutAndOrderedStages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "dbos.db"))
	require.NoError(t, err)
	database.SetMaxOpenConns(8)
	defer database.Close()

	dbosCtx, err := dbosgo.NewDBOSContext(ctx, dbosgo.Config{
		AppName: "opensight-queue-spike", SqliteSystemDB: database,
	})
	require.NoError(t, err)
	defer dbosgo.Shutdown(dbosCtx, 5*time.Second)

	accountID, _ := domain.NewID()
	businessID, _ := domain.NewID()
	runID, _ := domain.NewID()
	promptA, _ := domain.NewID()
	promptB, _ := domain.NewID()
	promptC, _ := domain.NewID()
	resultA, _ := domain.NewID()
	resultB, _ := domain.NewID()
	ops := &queueevaltest.Operations{
		AccountID: accountID, BusinessID: businessID, RunID: runID,
		Prompts:   []workflows.PromptSnapshot{{ID: promptA}, {ID: promptB}, {ID: promptC}},
		ResultIDs: []domain.ID{resultA, resultB}, WorkDelay: 30 * time.Millisecond,
	}
	runtime := &Runtime{Ops: ops, Name: "test-monitoring"}
	runtime.Register(dbosCtx)
	// A cap of one proves the analysis coordinator does not deadlock its child
	// extractions by occupying the only slot in the same queue.
	require.NoError(t, RegisterQueues(dbosCtx, 1))
	require.NoError(t, dbosgo.Launch(dbosCtx))

	handle, err := dbosgo.RunWorkflow(dbosCtx, runtime.Monitor, MonitorInput{
		BusinessID: businessID, Platform: store.PlatformChatGPT,
		ScheduledFor: time.Now().UTC(), Trigger: store.RunTriggerScheduled,
	}, dbosgo.WithRunInstance(runtime), dbosgo.WithWorkflowID("monitor-"+runID.String()))
	require.NoError(t, err)
	result, err := handle.GetResult()
	require.NoError(t, err)
	require.False(t, result.Skipped)
	require.False(t, result.AnalysisFailed)
	require.True(t, ops.Finalized)
	require.True(t, ops.Reconciled)
	require.True(t, ops.Assessed)
	require.Positive(t, ops.MaxPromptConcurrency())
	require.LessOrEqual(t, ops.MaxPromptConcurrency(), 1)
}
