package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthStore reads credentials and manages server-side sessions (design 07
// "Auth and accounts"). Unlike the business-anchored repositories, its methods
// are deliberately NOT tenant-scoped: they are the sanctioned unscoped path
// that *establishes* tenant context (the login/session equivalent of
// ResolveTenantID). Login looks up a user by email before any tenant is known,
// and a session token resolves to exactly one user/tenant. Keep the method set
// to exactly these four; anything tenant-scoped belongs on a business store.
//
// GetSession also resolves the tenant's billing state (BILL-6): it LEFT JOINs
// subscriptions so access is available anywhere a session is, with no extra
// round trip. This is a read only — GetSession never writes billing state.
type AuthStore struct {
	db *pgxpool.Pool
}

// NewAuthStore returns an AuthStore backed by db.
func NewAuthStore(db *pgxpool.Pool) *AuthStore {
	return &AuthStore{db: db}
}

// UserCredentials is the login-time view of a user joined to its tenant.
type UserCredentials struct {
	UserID       domain.ID
	TenantID     domain.ID
	Email        string
	TenantName   string
	PasswordHash *string // NULL until AUTH-2's CLI sets a credential.
}

// SessionUser is a live session resolved to its owning user and tenant, plus
// the tenant's billing state (BILL-6). PlanCode and Billing come from the
// same joined row as the rest of SessionUser — no second lookup. Billing is
// the raw State, not a derived Access: access must be computed per-request
// against the current time (billing.DeriveAccess), never cached on the
// session, or the dunning bound would stop taking effect immediately.
type SessionUser struct {
	UserID     domain.ID
	TenantID   domain.ID
	Email      string
	TenantName string
	ExpiresAt  time.Time
	PlanCode   string
	Billing    billing.State
}

// CreateSessionParams are the inputs for persisting a new session row. TokenHash
// must be the 32-byte SHA-256 of the raw cookie token — the raw token is never
// stored.
type CreateSessionParams struct {
	TokenHash []byte
	UserID    domain.ID
	ExpiresAt time.Time
}

// GetUserCredentials loads a user's credentials by email. Matching is
// case-insensitive because email is a citext column. An unknown email returns
// ErrNotFound; the login handler makes that indistinguishable from a wrong
// password so there is no user-existence oracle.
func (s *AuthStore) GetUserCredentials(ctx context.Context, email string) (UserCredentials, error) {
	if s == nil || s.db == nil {
		return UserCredentials{}, errors.New("auth store database is required")
	}

	row, err := queries(ctx, s.db).GetUserCredentials(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserCredentials{}, ErrNotFound
		}
		return UserCredentials{}, fmt.Errorf("get user credentials: %w", err)
	}
	return UserCredentials{UserID: row.ID, TenantID: row.TenantID, Email: row.Email, TenantName: row.Name, PasswordHash: row.PasswordHash}, nil
}

// CreateSession inserts the session row and, in the same transaction, purges
// this user's already-expired sessions. That opportunistic cleanup at login is
// the entire session-reaping story for MVP — no background job.
func (s *AuthStore) CreateSession(ctx context.Context, params CreateSessionParams) error {
	if s == nil || s.db == nil {
		return errors.New("auth store database is required")
	}
	if len(params.TokenHash) != 32 {
		return fmt.Errorf("session token hash must be 32 bytes, got %d", len(params.TokenHash))
	}
	if err := validateUUIDv7("user id", params.UserID); err != nil {
		return err
	}
	if params.ExpiresAt.IsZero() {
		return errors.New("session expiry is required")
	}

	return withTx(ctx, s.db, func(q *storesqlc.Queries) error {
		if err := q.InsertSession(ctx, storesqlc.InsertSessionParams{TokenHash: params.TokenHash, UserID: params.UserID, ExpiresAt: params.ExpiresAt}); err != nil {
			return fmt.Errorf("insert session: %w", err)
		}
		if err := q.DeleteExpiredUserSessions(ctx, params.UserID); err != nil {
			return fmt.Errorf("purge expired sessions: %w", err)
		}
		return nil
	})
}

// GetSession resolves a live (unexpired) session by token hash to its user,
// tenant, and billing state (BILL-6). An absent or expired session both
// return ErrNotFound — the two are deliberately indistinguishable so an
// expired token leaks nothing.
//
// The subscriptions join is a LEFT JOIN, deliberately: every tenant is
// supposed to have exactly one subscriptions row (signup creates it in the
// same transaction as the tenant), so a missing one is our bug, not an
// absent session. Masquerading it as ErrNotFound would silently log the user
// out instead of surfacing the inconsistency; this returns a distinct error
// instead, which the RPC layer maps to CodeInternal.
func (s *AuthStore) GetSession(ctx context.Context, tokenHash []byte) (SessionUser, error) {
	if s == nil || s.db == nil {
		return SessionUser{}, errors.New("auth store database is required")
	}
	if len(tokenHash) != 32 {
		return SessionUser{}, ErrNotFound
	}

	row, err := queries(ctx, s.db).GetSession(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionUser{}, ErrNotFound
		}
		return SessionUser{}, fmt.Errorf("get session: %w", err)
	}
	if row.PlanCode == nil {
		return SessionUser{}, fmt.Errorf("get session: tenant %s has no subscriptions row", row.TenantID)
	}

	return SessionUser{
		UserID: row.ID, TenantID: row.TenantID, Email: row.Email, TenantName: row.Name, ExpiresAt: row.ExpiresAt,
		PlanCode: *row.PlanCode,
		Billing:  billingStateFromRow(row.Comped.Valid && row.Comped.Bool, row.StripeSubscriptionID, row.StripeStatus, row.PastDueSince),
	}, nil
}

// DeleteSession removes a session row by token hash. A missing row is not an
// error, so logout is idempotent.
func (s *AuthStore) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if s == nil || s.db == nil {
		return errors.New("auth store database is required")
	}
	if len(tokenHash) != 32 {
		return nil
	}
	if err := queries(ctx, s.db).DeleteSession(ctx, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
