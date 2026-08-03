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
)

// The identity and session methods below are deliberately NOT tenant-scoped:
// they are the sanctioned unscoped path that *establishes* tenant context (the
// login/session equivalent of ResolveTenantID). The Google callback looks up a
// user by google_sub or email before any tenant is known, and a session token
// resolves to exactly one user/tenant.

// UserIdentity is the sign-in-time view of a user joined to its tenant.
type UserIdentity struct {
	UserID     domain.ID
	TenantID   domain.ID
	Email      string
	TenantName string
	GoogleSub  *string // NULL until the first Google sign-in links this row.
}

// SessionUser is a live session resolved to its owning user and tenant, plus
// the tenant's billing state. PlanCode and Billing come from the
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

// GetUserByGoogleSub loads a user by Google's stable subject claim — the fast
// path for every sign-in after the first. An unmatched sub returns
// ErrNotFound; the caller falls back to GetUserByEmail.
func (s *Store) GetUserByGoogleSub(ctx context.Context, sub string) (UserIdentity, error) {

	row, err := s.q(ctx).GetUserByGoogleSub(ctx, &sub)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserIdentity{}, ErrNotFound
		}
		return UserIdentity{}, fmt.Errorf("get user by google sub: %w", err)
	}
	return UserIdentity{UserID: row.ID, TenantID: row.TenantID, Email: row.Email, TenantName: row.Name, GoogleSub: row.GoogleSub}, nil
}

// GetUserByEmail loads a user by email. Matching is case-insensitive because
// email is a citext column. This is the first-Google-sign-in path: a row
// created by the operator CLI (or a prior Google account with the same
// address) has no google_sub yet, so the caller links it via SetUserGoogleSub.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (UserIdentity, error) {

	row, err := s.q(ctx).GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserIdentity{}, ErrNotFound
		}
		return UserIdentity{}, fmt.Errorf("get user by email: %w", err)
	}
	return UserIdentity{UserID: row.ID, TenantID: row.TenantID, Email: row.Email, TenantName: row.Name, GoogleSub: row.GoogleSub}, nil
}

// SetUserGoogleSub links a user row to its Google subject claim on first
// sign-in (either an operator-created row or a row found by email).
func (s *Store) SetUserGoogleSub(ctx context.Context, userID domain.ID, sub string) error {
	if err := s.q(ctx).SetUserGoogleSub(ctx, storesqlc.SetUserGoogleSubParams{ID: userID, GoogleSub: &sub}); err != nil {
		return fmt.Errorf("set user google sub: %w", err)
	}
	return nil
}

// CreateSession inserts the session row and, in the same transaction, purges
// this user's already-expired sessions. That opportunistic cleanup at login is
// the entire session-reaping story for MVP — no background job.
func (s *Store) CreateSession(ctx context.Context, params CreateSessionParams) error {
	if len(params.TokenHash) != 32 {
		return fmt.Errorf("session token hash must be 32 bytes, got %d", len(params.TokenHash))
	}
	if err := validateUUIDv7("user id", params.UserID); err != nil {
		return err
	}
	if params.ExpiresAt.IsZero() {
		return errors.New("session expiry is required")
	}

	return s.withTx(ctx, func(q *storesqlc.Queries) error {
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
// tenant, and billing state. An absent or expired session both
// return ErrNotFound — the two are deliberately indistinguishable so an
// expired token leaks nothing.
//
// The subscriptions join is a LEFT JOIN, deliberately: every tenant is
// supposed to have exactly one subscriptions row (signup creates it in the
// same transaction as the tenant), so a missing one is our bug, not an
// absent session. Masquerading it as ErrNotFound would silently log the user
// out instead of surfacing the inconsistency; this returns a distinct error
// instead, which the RPC layer maps to CodeInternal.
func (s *Store) GetSession(ctx context.Context, tokenHash []byte) (SessionUser, error) {
	if len(tokenHash) != 32 {
		return SessionUser{}, ErrNotFound
	}

	row, err := s.q(ctx).GetSession(ctx, tokenHash)
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
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if len(tokenHash) != 32 {
		return nil
	}
	if err := s.q(ctx).DeleteSession(ctx, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
