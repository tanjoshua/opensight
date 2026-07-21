package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/domain"

	"github.com/google/uuid"
)

const starterPlanSlug = "starter"

// Tenant is a persisted tenants row used by admin CLI account creation.
type Tenant struct {
	ID        domain.ID
	Name      string
	PlanID    domain.ID
	CreatedAt time.Time
}

// User is a persisted users row used by admin CLI account creation.
type User struct {
	ID        domain.ID
	TenantID  domain.ID
	Email     string
	CreatedAt time.Time
}

// Plan is a persisted plans row: a tenant's entitlements. run_interval drives
// the Temporal Schedule spec (RUN-5); prompt_limit bounds active prompts.
type Plan struct {
	ID          domain.ID
	Slug        string
	PromptLimit int
	RunInterval string
	Platforms   []string
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
	starterPlanIDSQL = `SELECT id FROM plans WHERE slug = $1`

	insertTenantSQL = `
INSERT INTO tenants (id, name, plan_id)
VALUES ($1, $2, $3)
RETURNING created_at`

	insertUserSQL = `
INSERT INTO users (id, tenant_id, email, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING created_at`

	// tenantPlanSQL loads a tenant's plan entitlements through the plan FK.
	// platforms is a text[]; it is read as JSON and unmarshalled (see stringSlice).
	tenantPlanSQL = `
SELECT p.id, p.slug, p.prompt_limit, p.run_interval, to_jsonb(p.platforms)
FROM tenants t
JOIN plans p ON p.id = t.plan_id
WHERE t.id = $1`
)

// CreateTenant creates a tenant on the seeded starter plan. Plan entitlements
// still come from the plans row; the CLI only chooses the starter slug.
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
		if err := q.queryRowContext(ctx, starterPlanIDSQL, starterPlanSlug).Scan(&tenant.PlanID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("starter plan is missing; run migrations: %w", ErrNotFound)
			}
			return fmt.Errorf("load starter plan: %w", err)
		}
		if err := q.queryRowContext(ctx, insertTenantSQL, tenant.ID, tenant.Name, tenant.PlanID).Scan(&tenant.CreatedAt); err != nil {
			return fmt.Errorf("insert tenant: %w", err)
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

// GetTenantPlan loads the tenant's plan (RUN-5 schedule creation derives the run
// interval from it). A missing tenant returns ErrNotFound.
func (s *AdminStore) GetTenantPlan(ctx context.Context, tenantID domain.ID) (Plan, error) {
	if s == nil || s.db == nil {
		return Plan{}, errors.New("admin store database is required")
	}

	var plan Plan
	var platforms stringSlice
	if err := s.db.QueryRowContext(ctx, tenantPlanSQL, tenantID).Scan(
		&plan.ID, &plan.Slug, &plan.PromptLimit, &plan.RunInterval, &platforms,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Plan{}, ErrNotFound
		}
		return Plan{}, fmt.Errorf("get tenant plan: %w", err)
	}
	plan.Platforms = platforms
	return plan, nil
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
