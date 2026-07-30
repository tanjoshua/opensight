package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

type advisoryConnectionContextKey struct{}

type contextDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func dbFromContext(ctx context.Context, fallback *sql.DB) contextDB {
	if conn, ok := ctx.Value(advisoryConnectionContextKey{}).(*sql.Conn); ok {
		return conn
	}
	return fallback
}

// AdvisoryLocker serializes work across every app instance using PostgreSQL
// session-level advisory locks. The dedicated connection is held for the
// callback's duration because advisory locks belong to a database session.
type AdvisoryLocker struct {
	db *sql.DB
}

// NewAdvisoryLocker returns an AdvisoryLocker backed by db.
func NewAdvisoryLocker(db *sql.DB) *AdvisoryLocker {
	return &AdvisoryLocker{db: db}
}

// WithLock holds the lock for key while fn runs. PostgreSQL hashes the
// namespaced text key to a signed 64-bit advisory-lock id; a hash collision
// can only serialize unrelated work, not compromise correctness.
func (l *AdvisoryLocker) WithLock(ctx context.Context, key string, fn func(context.Context) error) (err error) {
	if l == nil || l.db == nil {
		return errors.New("advisory locker database is required")
	}

	conn, err := l.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("advisory lock: acquire connection: %w", err)
	}
	defer func() {
		err = errors.Join(err, conn.Close())
	}()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, key); err != nil {
		// Cancellation can race the server acquiring the lock. Discarding
		// the session guarantees a possibly-acquired lock cannot leak back
		// into the pool.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return fmt.Errorf("advisory lock %q: acquire: %w", key, err)
	}

	defer func() {
		// The request context may have been cancelled while fn ran. Use a
		// short independent context so the pooled connection is never
		// returned while it still owns the session-level lock.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var unlocked bool
		unlockErr := conn.QueryRowContext(
			unlockCtx,
			`SELECT pg_advisory_unlock(hashtextextended($1, 0))`,
			key,
		).Scan(&unlocked)
		if unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("advisory lock %q: release: %w", key, unlockErr))
			// Never put a possibly still-locked session back into the pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		} else if !unlocked {
			err = errors.Join(err, fmt.Errorf("advisory lock %q: release: lock was not held", key))
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()

	// Store calls inside fn reuse the locked session. Besides making the lock
	// boundary explicit at the database, this prevents pool starvation when
	// many callbacks concurrently hold dedicated lock connections.
	return fn(context.WithValue(ctx, advisoryConnectionContextKey{}, conn))
}
