package jobs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"
	"opensight/internal/workflows"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

type recoveryArgs struct {
	Token string `json:"token"`
}

func TestProfileGenerationStagesFencingAndTerminalFailure(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run profile generation integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer pool.Close()
	accountID := mustJobID(t)
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,name,slug) VALUES ($1,'River Generation',$2)`, accountID, "river-generation-"+accountID.String()); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, accountID) }()
	if _, err := pool.Exec(ctx, `INSERT INTO subscriptions (account_id,plan_code,comped) VALUES ($1,$2,true)`, accountID, billing.Starter.Code); err != nil {
		t.Fatalf("insert subscription: %v", err)
	}
	repository := store.New(pool)

	newDraft := func(name string) store.Business {
		business, err := repository.CreateBusiness(ctx, store.CreateBusinessParams{AccountID: accountID, Name: name, Status: store.BusinessStatusDraft})
		if err != nil {
			t.Fatalf("create draft: %v", err)
		}
		return business
	}
	install := func(businessID, generationID domain.ID, jobID int64) {
		if err := repository.Transact(ctx, func(tx pgx.Tx) error {
			return repository.InstallGeneration(ctx, tx, accountID, businessID, generationID, jobID, nil)
		}); err != nil {
			t.Fatalf("install generation: %v", err)
		}
	}

	readyBusiness, readyGeneration := newDraft("Ready Profile"), mustJobID(t)
	install(readyBusiness.ID, readyGeneration, 701)
	readyOps := stubOperations{}
	readyOps.fetchSite = func(context.Context, workflows.FetchSiteInput) (workflows.FetchSiteOutput, error) {
		return workflows.FetchSiteOutput{Text: "site text"}, nil
	}
	readyOps.proposeProfile = func(context.Context, workflows.ProposeProfileInput) (workflows.ProposeProfileOutput, error) {
		return workflows.ProposeProfileOutput{Proposed: true, Payload: llm.ProposalPayload{}}, nil
	}
	readyWorker := &GenerateProfileWorker{Store: repository, Ops: readyOps}
	if err := readyWorker.Work(ctx, &river.Job[GenerateProfileArgs]{JobRow: &rivertype.JobRow{ID: 701, Attempt: 1, MaxAttempts: 2}, Args: GenerateProfileArgs{AccountID: accountID, BusinessID: readyBusiness.ID, GenerationID: readyGeneration}}); err != nil {
		t.Fatalf("ready generation: %v", err)
	}
	got, err := repository.GetBusiness(ctx, accountID, readyBusiness.ID)
	if err != nil || got.GenerationStatus == nil || *got.GenerationStatus != store.GenerationStatusReady || got.GenerationStage != nil {
		t.Fatalf("ready generation state = %+v, err=%v", got, err)
	}

	staleBusiness, staleGeneration, currentGeneration := newDraft("Stale Profile"), mustJobID(t), mustJobID(t)
	install(staleBusiness.ID, staleGeneration, 702)
	install(staleBusiness.ID, currentGeneration, 703)
	proposed := false
	staleOps := stubOperations{}
	staleOps.fetchSite = func(context.Context, workflows.FetchSiteInput) (workflows.FetchSiteOutput, error) {
		return workflows.FetchSiteOutput{}, nil
	}
	staleOps.proposeProfile = func(context.Context, workflows.ProposeProfileInput) (workflows.ProposeProfileOutput, error) {
		proposed = true
		return workflows.ProposeProfileOutput{Proposed: true}, nil
	}
	if err := (&GenerateProfileWorker{Store: repository, Ops: staleOps}).Work(ctx, &river.Job[GenerateProfileArgs]{JobRow: &rivertype.JobRow{ID: 702, Attempt: 1, MaxAttempts: 2}, Args: GenerateProfileArgs{AccountID: accountID, BusinessID: staleBusiness.ID, GenerationID: staleGeneration}}); err != nil || proposed {
		t.Fatalf("stale generation error/proposed = %v/%t", err, proposed)
	}

	failedBusiness, failedGeneration := newDraft("Failed Profile"), mustJobID(t)
	install(failedBusiness.ID, failedGeneration, 704)
	failedOps := stubOperations{}
	failedOps.fetchSite = func(context.Context, workflows.FetchSiteInput) (workflows.FetchSiteOutput, error) {
		return workflows.FetchSiteOutput{}, nil
	}
	failedOps.proposeProfile = func(context.Context, workflows.ProposeProfileInput) (workflows.ProposeProfileOutput, error) {
		return workflows.ProposeProfileOutput{}, errors.New("provider unavailable")
	}
	if err := (&GenerateProfileWorker{Store: repository, Ops: failedOps}).Work(ctx, &river.Job[GenerateProfileArgs]{JobRow: &rivertype.JobRow{ID: 704, Attempt: 2, MaxAttempts: 2}, Args: GenerateProfileArgs{AccountID: accountID, BusinessID: failedBusiness.ID, GenerationID: failedGeneration}}); err != nil {
		t.Fatalf("terminal generation: %v", err)
	}
	got, err = repository.GetBusiness(ctx, accountID, failedBusiness.ID)
	if err != nil || got.GenerationStatus == nil || *got.GenerationStatus != store.GenerationStatusFailed || got.GenerationStage != nil {
		t.Fatalf("failed generation state = %+v, err=%v", got, err)
	}
}

func mustJobID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	return id
}

func (recoveryArgs) Kind() string { return "opensight_test_restart_recovery" }

type recoveryWorker struct {
	river.WorkerDefaults[recoveryArgs]
	started chan struct{}
}

func (w *recoveryWorker) Work(ctx context.Context, job *river.Job[recoveryArgs]) error {
	if job.Attempt == 1 {
		close(w.started)
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (*recoveryWorker) NextRetry(*river.Job[recoveryArgs]) time.Time { return time.Now() }

func TestRiverTransactionExecutionAndRestartRecovery(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run River integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer pool.Close()

	var appMigration, riverSchema bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM goose_db_version WHERE version_id = 22 AND is_applied), to_regclass('river_job') IS NOT NULL`).Scan(&appMigration, &riverSchema); err != nil {
		t.Fatalf("check migrated schemas: %v", err)
	}
	if !appMigration || !riverSchema {
		t.Fatalf("migrations missing: application=%t river=%t", appMigration, riverSchema)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM river_job WHERE kind = $1`, (recoveryArgs{}).Kind()); err != nil {
		t.Fatalf("clear prior test jobs: %v", err)
	}

	insertOnly, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatalf("new insert client: %v", err)
	}
	rollbackErr := errors.New("force rollback")
	st := store.New(pool)
	err = st.Transact(ctx, func(tx pgx.Tx) error {
		if _, err := insertOnly.InsertTx(ctx, tx, recoveryArgs{Token: "rolled-back"}, nil); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("transaction error = %v, want forced rollback", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = $1`, (recoveryArgs{}).Kind()).Scan(&count); err != nil {
		t.Fatalf("count rolled-back jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back jobs = %d, want 0", count)
	}

	started := make(chan struct{})
	client1 := newRecoveryClient(t, pool, &recoveryWorker{started: started})
	if _, err := client1.Insert(ctx, recoveryArgs{Token: "recover-after-restart"}, nil); err != nil {
		t.Fatalf("insert recovery job: %v", err)
	}
	if err := client1.Start(ctx); err != nil {
		t.Fatalf("start first client: %v", err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatalf("wait for first attempt: %v", ctx.Err())
	}
	if err := client1.StopAndCancel(context.WithoutCancel(ctx)); err != nil {
		t.Fatalf("stop first client: %v", err)
	}

	client2 := newRecoveryClient(t, pool, &recoveryWorker{started: make(chan struct{})})
	completed, unsubscribe := client2.Subscribe(river.EventKindJobCompleted)
	defer unsubscribe()
	if err := client2.Start(ctx); err != nil {
		t.Fatalf("start replacement client: %v", err)
	}
	defer func() { _ = client2.StopAndCancel(context.Background()) }()
	select {
	case event := <-completed:
		if event.Job.Kind != (recoveryArgs{}).Kind() || event.Job.Attempt < 2 {
			t.Fatalf("completed job kind/attempt = %s/%d, want recovery attempt", event.Job.Kind, event.Job.Attempt)
		}
	case <-ctx.Done():
		t.Fatalf("wait for recovered job: %v", ctx.Err())
	}
}

func newRecoveryClient(t *testing.T, pool *pgxpool.Pool, worker *recoveryWorker) *river.Client[pgx.Tx] {
	t.Helper()
	workers := river.NewWorkers()
	river.AddWorker(workers, worker)
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
		Workers: workers,
	})
	if err != nil {
		t.Fatalf("new River client: %v", err)
	}
	return client
}
