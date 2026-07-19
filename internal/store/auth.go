package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opensight/internal/domain"
)

// AuthStore reads credentials and manages server-side sessions (design 07
// "Auth and accounts"). Unlike the business-anchored repositories, its methods
// are deliberately NOT tenant-scoped: they are the sanctioned unscoped path
// that *establishes* tenant context (the login/session equivalent of
// ResolveTenantID). Login looks up a user by email before any tenant is known,
// and a session token resolves to exactly one user/tenant. Keep the method set
// to exactly these four; anything tenant-scoped belongs on a business store.
type AuthStore struct {
	db *sql.DB
}

// NewAuthStore returns an AuthStore backed by db.
func NewAuthStore(db *sql.DB) *AuthStore {
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

// SessionUser is a live session resolved to its owning user and tenant.
type SessionUser struct {
	UserID     domain.ID
	TenantID   domain.ID
	Email      string
	TenantName string
	ExpiresAt  time.Time
}

// CreateSessionParams are the inputs for persisting a new session row. TokenHash
// must be the 32-byte SHA-256 of the raw cookie token — the raw token is never
// stored.
type CreateSessionParams struct {
	TokenHash []byte
	UserID    domain.ID
	ExpiresAt time.Time
}

const (
	getUserCredentialsSQL = `
SELECT u.id, u.tenant_id, u.email, t.name, u.password_hash
FROM users u
JOIN tenants t ON t.id = u.tenant_id
WHERE u.email = $1`

	insertSessionSQL = `
INSERT INTO sessions (token_hash, user_id, expires_at)
VALUES ($1, $2, $3)`

	deleteExpiredUserSessionsSQL = `
DELETE FROM sessions
WHERE user_id = $1 AND expires_at <= now()`

	getSessionSQL = `
SELECT u.id, u.tenant_id, u.email, t.name, s.expires_at
FROM sessions s
JOIN users u ON u.id = s.user_id
JOIN tenants t ON t.id = u.tenant_id
WHERE s.token_hash = $1 AND s.expires_at > now()`

	deleteSessionSQL = `DELETE FROM sessions WHERE token_hash = $1`
)

// GetUserCredentials loads a user's credentials by email. Matching is
// case-insensitive because email is a citext column. An unknown email returns
// ErrNotFound; the login handler makes that indistinguishable from a wrong
// password so there is no user-existence oracle.
func (s *AuthStore) GetUserCredentials(ctx context.Context, email string) (UserCredentials, error) {
	if s == nil || s.db == nil {
		return UserCredentials{}, errors.New("auth store database is required")
	}

	var creds UserCredentials
	err := s.db.QueryRowContext(ctx, getUserCredentialsSQL, email).Scan(
		&creds.UserID,
		&creds.TenantID,
		&creds.Email,
		&creds.TenantName,
		&creds.PasswordHash,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserCredentials{}, ErrNotFound
		}
		return UserCredentials{}, fmt.Errorf("get user credentials: %w", err)
	}
	return creds, nil
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

	return withTx(ctx, s.db, func(q querier) error {
		if _, err := q.execContext(ctx, insertSessionSQL, params.TokenHash, params.UserID, params.ExpiresAt); err != nil {
			return fmt.Errorf("insert session: %w", err)
		}
		if _, err := q.execContext(ctx, deleteExpiredUserSessionsSQL, params.UserID); err != nil {
			return fmt.Errorf("purge expired sessions: %w", err)
		}
		return nil
	})
}

// GetSession resolves a live (unexpired) session by token hash to its user and
// tenant. An absent or expired session both return ErrNotFound — the two are
// deliberately indistinguishable so an expired token leaks nothing.
func (s *AuthStore) GetSession(ctx context.Context, tokenHash []byte) (SessionUser, error) {
	if s == nil || s.db == nil {
		return SessionUser{}, errors.New("auth store database is required")
	}
	if len(tokenHash) != 32 {
		return SessionUser{}, ErrNotFound
	}

	var su SessionUser
	err := s.db.QueryRowContext(ctx, getSessionSQL, tokenHash).Scan(
		&su.UserID,
		&su.TenantID,
		&su.Email,
		&su.TenantName,
		&su.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionUser{}, ErrNotFound
		}
		return SessionUser{}, fmt.Errorf("get session: %w", err)
	}
	return su, nil
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
	if _, err := s.db.ExecContext(ctx, deleteSessionSQL, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
