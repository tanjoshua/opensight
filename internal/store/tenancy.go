package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opensight/internal/domain"

	"github.com/google/uuid"
)

// ErrNotFound is the single sentinel returned whenever a business-owned row is
// absent OR belongs to another tenant. "Doesn't exist" and "exists but isn't
// yours" are deliberately indistinguishable so the repository layer never acts
// as a cross-tenant existence oracle (design 01 D4, design 02).
var ErrNotFound = errors.New("not found")

// rowScanner is the single-row seam. *sql.Row satisfies it, and unit tests can
// fake it — which is why the package interface returns rowScanner rather than
// the concrete, unfakeable *sql.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

// querier is the shared read/write seam used by every repository so the
// tenant-checked helpers work identically over *sql.DB and *sql.Tx (adapted by
// sqlQuerier) and over unit-test fakes.
type querier interface {
	queryRowContext(ctx context.Context, query string, args ...any) rowScanner
	queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	execContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// sqlQueryer is the subset of *sql.DB / *sql.Tx that sqlQuerier adapts.
type sqlQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// sqlQuerier adapts a live *sql.DB or *sql.Tx to querier.
type sqlQuerier struct {
	q sqlQueryer
}

func (s sqlQuerier) queryRowContext(ctx context.Context, query string, args ...any) rowScanner {
	return s.q.QueryRowContext(ctx, query, args...)
}

func (s sqlQuerier) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.q.QueryContext(ctx, query, args...)
}

func (s sqlQuerier) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.q.ExecContext(ctx, query, args...)
}

const businessOwnedSQL = `SELECT 1 FROM businesses WHERE id = $1 AND tenant_id = $2`

// businessOwned is the tenant-checked business lookup that every
// business-anchored repository method (lists and writes) enters through
// (design 02). It returns ErrNotFound unless the business exists and belongs to
// tenantID. Writes call it inside their own transaction so the ownership check
// and the mutation are atomic.
func businessOwned(ctx context.Context, q querier, tenantID, businessID domain.ID) error {
	var one int
	if err := q.queryRowContext(ctx, businessOwnedSQL, businessID, tenantID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("verify business ownership: %w", err)
	}
	return nil
}

// withTx runs fn inside a transaction, committing on success and rolling back on
// error. Every write method uses it so its businessOwned/ownership check and its
// mutation share one transaction.
func withTx(ctx context.Context, db *sql.DB, fn func(q querier) error) error {
	if db == nil {
		return errors.New("store database is required")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := fn(sqlQuerier{q: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

func validateUUIDv7(name string, id domain.ID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%s is required", name)
	}
	if id.Version() != uuid.Version(7) {
		return fmt.Errorf("%s must be UUIDv7", name)
	}
	return nil
}
