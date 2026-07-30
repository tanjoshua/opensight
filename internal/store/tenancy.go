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

var ErrNotFound = errors.New("not found")

type connectionContextKey struct{}

func queries(ctx context.Context, pool *pgxpool.Pool) *storesqlc.Queries {
	if conn, ok := ctx.Value(connectionContextKey{}).(*pgxpool.Conn); ok {
		return storesqlc.New(conn)
	}
	return storesqlc.New(pool)
}

func businessOwned(ctx context.Context, q *storesqlc.Queries, tenantID, businessID uuid.UUID) error {
	_, err := q.BusinessOwned(ctx, storesqlc.BusinessOwnedParams{ID: businessID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("verify business ownership: %w", err)
	}
	return nil
}

type beginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func withTx(ctx context.Context, pool *pgxpool.Pool, fn func(*storesqlc.Queries) error) error {
	if pool == nil {
		return errors.New("store database is required")
	}
	var db beginner = pool
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

func validateUUIDv7(name string, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%s is required", name)
	}
	if id.Version() != uuid.Version(7) {
		return fmt.Errorf("%s must be UUIDv7", name)
	}
	return nil
}
