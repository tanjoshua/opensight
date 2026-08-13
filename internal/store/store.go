package store

import (
	"context"
	"errors"
	"fmt"

	storesqlc "opensight/internal/store/sqlc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound                = errors.New("not found")
	ErrInvalidActionTransition = errors.New("invalid action status transition")
)

// Store is the single Postgres repository: one connection pool, every query
// method. A Store that exists is valid — New is the only way to build one and
// it is called once at startup, so the methods do not re-check the pool.
type Store struct {
	db *pgxpool.Pool
}

// New returns a Store backed by db.
func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

type connectionContextKey struct{}

// q returns the generated query set bound to the connection ctx pins, if any
// (WithLock pins one), and to the pool otherwise.
func (s *Store) q(ctx context.Context) *storesqlc.Queries {
	if conn, ok := ctx.Value(connectionContextKey{}).(*pgxpool.Conn); ok {
		return storesqlc.New(conn)
	}
	return storesqlc.New(s.db)
}

type beginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// withTx runs fn inside a transaction on ctx's pinned connection, or the pool.
func (s *Store) withTx(ctx context.Context, fn func(*storesqlc.Queries) error) error {
	var db beginner = s.db
	if conn, ok := ctx.Value(connectionContextKey{}).(*pgxpool.Conn); ok {
		db = conn
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(storesqlc.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Transact exposes the process's transaction boundary to application services
// that must atomically combine store writes with River InsertTx calls.
func (s *Store) Transact(ctx context.Context, fn func(pgx.Tx) error) error {
	var db beginner = s.db
	if conn, ok := ctx.Value(connectionContextKey{}).(*pgxpool.Conn); ok {
		db = conn
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// WithLock serializes work across app instances behind a Postgres advisory
// lock. The callback receives a context pinned to the session that owns the
// lock, so every query and transaction inside it uses that same connection.
func (s *Store) WithLock(ctx context.Context, key string, fn func(context.Context) error) (err error) {
	conn, err := s.db.Acquire(ctx)
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

// businessOwned is the account-ownership gate every business-scoped write and
// list runs first, in the same transaction.
func businessOwned(ctx context.Context, q *storesqlc.Queries, accountID, businessID uuid.UUID) error {
	_, err := q.BusinessOwned(ctx, storesqlc.BusinessOwnedParams{ID: businessID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("verify business ownership: %w", err)
	}
	return nil
}

func validateUUIDv7(name string, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%s is required", name)
	}
	if id.Version() != uuid.Version(7) {
		return fmt.Errorf("%s must be UUIDv7", name)
	}
	return nil
}
