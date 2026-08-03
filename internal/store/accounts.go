package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

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

// ErrEmailTaken is returned when a signup hits the users.email unique index.
var ErrEmailTaken = errors.New("email already registered")

// CreateTenantParams are the inputs for creating a tenant on the starter plan.
type CreateTenantParams struct {
	ID   domain.ID
	Name string
}

// CreateUserParams are the inputs for creating an invite-only user. GoogleSub
// is normally empty: an operator-created row is linked to a Google account on
// its first sign-in (store.SetUserGoogleSub), not at creation time.
type CreateUserParams struct {
	ID        domain.ID
	TenantID  domain.ID
	Email     string
	GoogleSub string
}

// CreateTenant creates a tenant and its subscription row, in one transaction.
// Account creation — this method, CreateUser, and CreateAccount — is
// intentionally tenant-unscoped: it creates the tenant context every other
// method requires.
// CLI-provisioned tenants are operator tenants: comped = true, no Stripe
// objects (design 08 "Operator comps").
func (s *Store) CreateTenant(ctx context.Context, params CreateTenantParams) (Tenant, error) {

	params, err := normalizeCreateTenantParams(params)
	if err != nil {
		return Tenant{}, err
	}

	tenant := Tenant{
		ID:   params.ID,
		Name: params.Name,
	}
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		tenant.CreatedAt, err = q.InsertTenant(ctx, storesqlc.InsertTenantParams{ID: tenant.ID, Name: tenant.Name})
		if err != nil {
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

// CreateUser creates a user under an existing tenant.
func (s *Store) CreateUser(ctx context.Context, params CreateUserParams) (User, error) {

	params, err := normalizeCreateUserParams(params)
	if err != nil {
		return User{}, err
	}

	user := User{
		ID:       params.ID,
		TenantID: params.TenantID,
		Email:    params.Email,
	}
	user.CreatedAt, err = s.q(ctx).InsertUser(ctx, storesqlc.InsertUserParams{
		ID: user.ID, TenantID: user.TenantID, Email: user.Email, GoogleSub: nilIfEmpty(params.GoogleSub),
	})
	if err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return user, nil
}

// CreateAccountParams are the inputs for provisioning a tenant on a user's
// first Google sign-in.
type CreateAccountParams struct {
	TenantID  domain.ID
	UserID    domain.ID
	Email     string
	GoogleSub string
}

// CreateAccount creates a tenant, its starter subscription, and its first
// user in one transaction (design 08 "Signup"), so a half-created account is
// impossible: any failure rolls back the whole thing. Unlike CreateTenant
// (CLI-provisioned, comped = true), self-serve accounts are never comped and
// carry no Stripe objects. tenants.name is seeded from the email's local
// part; onboarding renames it to the business name (CreateBusiness).
func (s *Store) CreateAccount(ctx context.Context, params CreateAccountParams) (Tenant, User, error) {

	params, tenantName, err := normalizeCreateAccountParams(params)
	if err != nil {
		return Tenant{}, User{}, err
	}

	tenant := Tenant{ID: params.TenantID, Name: tenantName}
	user := User{ID: params.UserID, TenantID: params.TenantID, Email: params.Email}

	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		tenant.CreatedAt, err = q.InsertTenant(ctx, storesqlc.InsertTenantParams{ID: tenant.ID, Name: tenant.Name})
		if err != nil {
			return fmt.Errorf("insert tenant: %w", err)
		}
		if err := CreateSubscriptionInTx(ctx, q, tenant.ID, billing.Starter.Code, false); err != nil {
			return err
		}
		user.CreatedAt, err = q.InsertUser(ctx, storesqlc.InsertUserParams{
			ID: user.ID, TenantID: user.TenantID, Email: user.Email, GoogleSub: &params.GoogleSub,
		})
		if err != nil {
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

// nilIfEmpty returns nil for an empty string, otherwise a pointer to s — used
// for InsertUser's optional google_sub column.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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
	params.GoogleSub = strings.TrimSpace(params.GoogleSub)
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

	params.GoogleSub = strings.TrimSpace(params.GoogleSub)
	if params.GoogleSub == "" {
		return CreateAccountParams{}, "", errors.New("google sub is required")
	}
	return params, local, nil
}
