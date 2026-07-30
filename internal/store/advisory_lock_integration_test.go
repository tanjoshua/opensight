package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestAdvisoryLockerSerializesSameKey(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Prove callback store work reuses the locked session rather than waiting
	// forever for a second pooled connection.
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "test-advisory-lock:" + mustNewID(t).String()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	errs := make(chan error, 2)

	go func() {
		errs <- NewAdvisoryLocker(db).WithLock(ctx, key, func(lockedCtx context.Context) error {
			var one int
			if err := dbFromContext(lockedCtx, db).QueryRowContext(lockedCtx, `SELECT 1`).Scan(&one); err != nil {
				return err
			}
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered

	go func() {
		errs <- NewAdvisoryLocker(db).WithLock(ctx, key, func(context.Context) error {
			close(secondEntered)
			return nil
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("second callback entered while the first still held the lock")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case <-secondEntered:
	case <-ctx.Done():
		t.Fatal("second callback did not enter after the first released the lock")
	}

	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("WithLock: %v", err)
		}
	}
}
