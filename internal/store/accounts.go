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

// AdminStore owns invite-only account creation. It is intentionally
// tenant-unscoped because these commands create the tenant context that normal
// repositories later require.
type AdminStore struct {
	db *sql.DB
}

// NewAdminStore returns an AdminStore backed by db.
func NewAdminStore(db *sql.DB) *AdminStore {
	return &AdminStore{db: db}
}

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
func (s *AdminStore) CreateTenant(ctx context.Context, params CreateTenantParams) (Tenant, error) {
	if s == nil || s.db == nil {
		return Tenant{}, errors.New("admin store database is required")
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
func (s *AdminStore) CreateUser(ctx context.Context, params CreateUserParams) (User, error) {
	if s == nil || s.db == nil {
		return User{}, errors.New("admin store database is required")
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
