package store

import (
	"context"
	"errors"
	"fmt"

	storesqlc "opensight/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdvisoryLocker serializes work across app instances. The callback receives a
// context pinned to the session that owns the lock, so every repository query
// and transaction inside it uses that same connection.
type AdvisoryLocker struct{ pool *pgxpool.Pool }

func NewAdvisoryLocker(pool *pgxpool.Pool) *AdvisoryLocker { return &AdvisoryLocker{pool: pool} }

func (l *AdvisoryLocker) WithLock(ctx context.Context, key string, fn func(context.Context) error) (err error) {
	if l == nil || l.pool == nil {
		return errors.New("advisory locker database is required")
	}
	if key == "" {
		return errors.New("advisory lock key is required")
	}
	if fn == nil {
		return errors.New("advisory lock callback is required")
	}
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire advisory connection: %w", err)
	}
	reusable := false
	defer func() {
		if reusable {
			conn.Release()
			return
		}
		raw := conn.Hijack()
		_ = raw.Close(context.WithoutCancel(ctx))
	}()

	q := storesqlc.New(conn)
	if err := q.AcquireAdvisoryLock(ctx, key); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	defer func() {
		unlocked, unlockErr := q.ReleaseAdvisoryLock(context.WithoutCancel(ctx), key)
		if unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("release advisory lock: %w", unlockErr))
			return
		}
		if !unlocked {
			err = errors.Join(err, errors.New("release advisory lock: lock was not held"))
			return
		}
		reusable = true
	}()

	return fn(context.WithValue(ctx, connectionContextKey{}, conn))
}
