package store

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The pause is only worth anything if it actually removes the business from
// the scheduler sweep's candidate set — that filter lives in SQL, so this is
// the only place it can be proven. The pause/resume round trip and its
// idempotency ride along in the same fixture.
func TestMonitoringPauseRemovesBusinessFromSchedulerCandidates(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	accountID := mustNewID(t)
	businessID := mustNewID(t)
	draftID := mustNewID(t)

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Monitoring Pause Account")
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Pause Clinic', 'clinic', '{"country":"SG"}'::jsonb, now())`, businessID, accountID)
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name)
		VALUES ($1, $2, 'draft', 'Pause Draft')`, draftID, accountID)

	businessStore := New(db)
	isCandidate := func() bool {
		list, err := businessStore.ListMonitoringCandidates(ctx)
		if err != nil {
			t.Fatalf("list monitoring candidates: %v", err)
		}
		return slices.ContainsFunc(list, func(c MonitoringCandidate) bool { return c.BusinessID == businessID })
	}

	if !isCandidate() {
		t.Fatal("active business is not a monitoring candidate before pausing")
	}

	paused, err := businessStore.SetMonitoringPaused(ctx, accountID, businessID, true)
	if err != nil {
		t.Fatalf("pause monitoring: %v", err)
	}
	if paused.MonitoringPausedAt == nil {
		t.Fatal("paused business has no monitoring_paused_at")
	}
	if isCandidate() {
		t.Fatal("paused business is still a monitoring candidate")
	}

	// Re-pausing must not restart the "paused since" clock the UI shows.
	repaused, err := businessStore.SetMonitoringPaused(ctx, accountID, businessID, true)
	if err != nil {
		t.Fatalf("re-pause monitoring: %v", err)
	}
	if !repaused.MonitoringPausedAt.Equal(*paused.MonitoringPausedAt) {
		t.Errorf("re-pause moved monitoring_paused_at from %v to %v", *paused.MonitoringPausedAt, *repaused.MonitoringPausedAt)
	}

	resumed, err := businessStore.SetMonitoringPaused(ctx, accountID, businessID, false)
	if err != nil {
		t.Fatalf("resume monitoring: %v", err)
	}
	if resumed.MonitoringPausedAt != nil {
		t.Errorf("resumed business monitoring_paused_at = %v, want nil", *resumed.MonitoringPausedAt)
	}
	if !isCandidate() {
		t.Error("resumed business is not a monitoring candidate again")
	}

	if _, err := businessStore.SetMonitoringPaused(ctx, accountID, draftID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("pause draft business error = %v, want ErrNotFound", err)
	}
	if _, err := businessStore.SetMonitoringPaused(ctx, mustNewID(t), businessID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account pause error = %v, want ErrNotFound", err)
	}
}
