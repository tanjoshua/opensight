package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"

	"github.com/google/uuid"
)

// Tenant is a persisted tenants row used by admin CLI account creation.
type Tenant struct {
	ID        domain.ID
	Name      string
	CreatedAt time.Time
}

// User is a persisted users row used by admin CLI account creation.
type User struct {
	ID        domain.ID
	TenantID  domain.ID
	Email     string
	CreatedAt time.Time
}

// AccountStore owns account creation: both operator-provisioned (CLI,
// invite-only) and self-serve (signup) tenants. It is intentionally
// tenant-unscoped because these commands create the tenant context that normal
// repositories later require.
type AccountStore struct {
	db *sql.DB
}

// NewAccountStore returns an AccountStore backed by db.
func NewAccountStore(db *sql.DB) *AccountStore {
	return &AccountStore{db: db}
}

// ErrEmailTaken is returned when a signup hits the users.email unique index.
var ErrEmailTaken = errors.New("email already registered")

// CreateTenantParams are the inputs for creating a tenant on the starter plan.
type CreateTenantParams struct {
	ID   domain.ID
	Name string
}

// CreateUserParams are the inputs for creating an invite-only user credential.
type CreateUserParams struct {
	ID           domain.ID
	TenantID     domain.ID
	Email        string
	PasswordHash string
}

const (
	insertTenantSQL = `
INSERT INTO tenants (id, name)
VALUES ($1, $2)
RETURNING created_at`

	insertUserSQL = `
INSERT INTO users (id, tenant_id, email, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING created_at`
)

// CreateTenant creates a tenant and its subscription row, in one transaction.
// CLI-provisioned tenants are operator tenants: comped = true, no Stripe
// objects (design 08 "Operator comps").
func (s *AccountStore) CreateTenant(ctx context.Context, params CreateTenantParams) (Tenant, error) {
	if s == nil || s.db == nil {
		return Tenant{}, errors.New("account store database is required")
	}

	params, err := normalizeCreateTenantParams(params)
	if err != nil {
		return Tenant{}, err
	}

	tenant := Tenant{
		ID:   params.ID,
		Name: params.Name,
	}
	err = withTx(ctx, s.db, func(q querier) error {
		if err := q.queryRowContext(ctx, insertTenantSQL, tenant.ID, tenant.Name).Scan(&tenant.CreatedAt); err != nil {
			return fmt.Errorf("insert tenant: %w", err)
		}
		if err := CreateSubscriptionInTx(ctx, q, tenant.ID, billing.Starter.Code, true); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Tenant{}, err
	}
	return tenant, nil
}

// CreateUser creates a user under an existing tenant with a pre-hashed password.
// The raw password belongs to the CLI layer and is never accepted here.
func (s *AccountStore) CreateUser(ctx context.Context, params CreateUserParams) (User, error) {
	if s == nil || s.db == nil {
		return User{}, errors.New("account store database is required")
	}

	params, err := normalizeCreateUserParams(params)
	if err != nil {
		return User{}, err
	}

	user := User{
		ID:       params.ID,
		TenantID: params.TenantID,
		Email:    params.Email,
	}
	if err := s.db.QueryRowContext(ctx, insertUserSQL, user.ID, user.TenantID, user.Email, params.PasswordHash).Scan(&user.CreatedAt); err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return user, nil
}

// CreateAccountParams are the inputs for a self-serve signup.
type CreateAccountParams struct {
	TenantID     domain.ID
	UserID       domain.ID
	Email        string
	PasswordHash string // pre-hashed; raw passwords never reach the store
}

// CreateAccount creates a tenant, its starter subscription, and its first
// user in one transaction (design 08 "Signup"), so a half-created account is
// impossible: any failure rolls back the whole thing. Unlike CreateTenant
// (CLI-provisioned, comped = true), self-serve accounts are never comped and
// carry no Stripe objects. tenants.name is seeded from the email's local
// part; onboarding renames it to the business name (BusinessStore.CreateBusiness).
func (s *AccountStore) CreateAccount(ctx context.Context, params CreateAccountParams) (Tenant, User, error) {
	if s == nil || s.db == nil {
		return Tenant{}, User{}, errors.New("account store database is required")
	}

	params, tenantName, err := normalizeCreateAccountParams(params)
	if err != nil {
		return Tenant{}, User{}, err
	}

	tenant := Tenant{ID: params.TenantID, Name: tenantName}
	user := User{ID: params.UserID, TenantID: params.TenantID, Email: params.Email}

	err = withTx(ctx, s.db, func(q querier) error {
		if err := q.queryRowContext(ctx, insertTenantSQL, tenant.ID, tenant.Name).Scan(&tenant.CreatedAt); err != nil {
			return fmt.Errorf("insert tenant: %w", err)
		}
		if err := CreateSubscriptionInTx(ctx, q, tenant.ID, billing.Starter.Code, false); err != nil {
			return err
		}
		if err := q.queryRowContext(ctx, insertUserSQL, user.ID, user.TenantID, user.Email, params.PasswordHash).Scan(&user.CreatedAt); err != nil {
			if isUniqueViolation(err) {
				return ErrEmailTaken
			}
			return fmt.Errorf("insert user: %w", err)
		}
		return nil
	})
	if err != nil {
		return Tenant{}, User{}, err
	}
	return tenant, user, nil
}

func normalizeCreateTenantParams(params CreateTenantParams) (CreateTenantParams, error) {
	if params.ID == uuid.Nil {
		id, err := domain.NewID()
		if err != nil {
			return CreateTenantParams{}, err
		}
		params.ID = id
	}
	if err := validateUUIDv7("tenant id", params.ID); err != nil {
		return CreateTenantParams{}, err
	}
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return CreateTenantParams{}, errors.New("tenant name is required")
	}
	return params, nil
}

func normalizeCreateUserParams(params CreateUserParams) (CreateUserParams, error) {
	if params.ID == uuid.Nil {
		id, err := domain.NewID()
		if err != nil {
			return CreateUserParams{}, err
		}
		params.ID = id
	}
	if err := validateUUIDv7("user id", params.ID); err != nil {
		return CreateUserParams{}, err
	}
	if err := validateUUIDv7("tenant id", params.TenantID); err != nil {
		return CreateUserParams{}, err
	}
	params.Email = strings.ToLower(strings.TrimSpace(params.Email))
	if params.Email == "" {
		return CreateUserParams{}, errors.New("user email is required")
	}
	params.PasswordHash = strings.TrimSpace(params.PasswordHash)
	if params.PasswordHash == "" {
		return CreateUserParams{}, errors.New("password hash is required")
	}
	return params, nil
}

// normalizeCreateAccountParams mints UUIDv7 IDs when zero, lowercases and
// trims the email, and derives the seed tenant name from its local part
// (design 08: "tenants.name defaults to the email local part").
func normalizeCreateAccountParams(params CreateAccountParams) (CreateAccountParams, string, error) {
	if params.TenantID == uuid.Nil {
		id, err := domain.NewID()
		if err != nil {
			return CreateAccountParams{}, "", err
		}
		params.TenantID = id
	}
	if err := validateUUIDv7("tenant id", params.TenantID); err != nil {
		return CreateAccountParams{}, "", err
	}
	if params.UserID == uuid.Nil {
		id, err := domain.NewID()
		if err != nil {
			return CreateAccountParams{}, "", err
		}
		params.UserID = id
	}
	if err := validateUUIDv7("user id", params.UserID); err != nil {
		return CreateAccountParams{}, "", err
	}

	params.Email = strings.ToLower(strings.TrimSpace(params.Email))
	local, domainPart, ok := strings.Cut(params.Email, "@")
	if !ok || local == "" || domainPart == "" {
		return CreateAccountParams{}, "", errors.New("a valid email is required")
	}

	params.PasswordHash = strings.TrimSpace(params.PasswordHash)
	if params.PasswordHash == "" {
		return CreateAccountParams{}, "", errors.New("password hash is required")
	}
	return params, local, nil
}
