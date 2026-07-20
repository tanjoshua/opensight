package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/domain"

	"github.com/google/uuid"
)

// BusinessStatus is the persisted lifecycle status for a business.
type BusinessStatus string

const (
	// BusinessStatusDraft is a business still in onboarding, before activation.
	BusinessStatusDraft BusinessStatus = "draft"
	// BusinessStatusActive is an activated business included in monitoring.
	BusinessStatusActive BusinessStatus = "active"
)

// Business is a persisted business row (migration 00003). tenant_id is the
// tenancy anchor every deeper table scopes through.
type Business struct {
	ID            domain.ID
	TenantID      domain.ID
	Status        BusinessStatus
	Name          string
	Website       *string
	Aliases       []string
	Category      *string
	Practitioners json.RawMessage
	Services      json.RawMessage
	Location      json.RawMessage
	CreatedAt     time.Time
	ActivatedAt   *time.Time
}

// CreateBusinessParams are the inputs for creating a business. TenantID is
// required (tenant existence is enforced by the FK). If ID is uuid.Nil a UUIDv7
// is generated. Nil Practitioners/Services default to an empty JSON array; nil
// Aliases default to an empty array; Location stays NULL when nil.
type CreateBusinessParams struct {
	ID            domain.ID
	TenantID      domain.ID
	Status        BusinessStatus
	Name          string
	Website       *string
	Aliases       []string
	Category      *string
	Practitioners json.RawMessage
	Services      json.RawMessage
	Location      json.RawMessage
	ActivatedAt   *time.Time
}

// BusinessStore reads and writes business rows. It is the tenant-checked entry
// point every deeper repository call funnels through.
type BusinessStore struct {
	db *sql.DB
}

// NewBusinessStore returns a BusinessStore backed by db.
func NewBusinessStore(db *sql.DB) *BusinessStore {
	return &BusinessStore{db: db}
}

const (
	insertBusinessSQL = `
INSERT INTO businesses (
  id, tenant_id, status, name, website, aliases, category,
  practitioners, services, location, activated_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10::jsonb, $11
)
RETURNING created_at`

	// aliases is a text[] column; pgx's database/sql driver encodes []string on
	// insert but will not decode text[] back into []string, so it is read as
	// JSON and unmarshalled (see stringSlice).
	businessColumns = `id, tenant_id, status, name, website, to_jsonb(aliases) AS aliases, category,
       practitioners, services, location, created_at, activated_at`

	getBusinessSQL = `
SELECT ` + businessColumns + `
FROM businesses
WHERE id = $1 AND tenant_id = $2`

	listBusinessesSQL = `
SELECT ` + businessColumns + `
FROM businesses
WHERE tenant_id = $1
ORDER BY created_at`

	resolveTenantIDSQL = `SELECT tenant_id FROM businesses WHERE id = $1`
)

// CreateBusiness inserts a business owned by params.TenantID (RUN-5 CLI
// `opensight business create`). Tenant existence is enforced by the FK.
func (s *BusinessStore) CreateBusiness(ctx context.Context, params CreateBusinessParams) (Business, error) {
	if s == nil || s.db == nil {
		return Business{}, errors.New("business store database is required")
	}

	params, err := normalizeCreateBusinessParams(params)
	if err != nil {
		return Business{}, err
	}

	business := Business{
		ID:            params.ID,
		TenantID:      params.TenantID,
		Status:        params.Status,
		Name:          params.Name,
		Website:       params.Website,
		Aliases:       params.Aliases,
		Category:      params.Category,
		Practitioners: params.Practitioners,
		Services:      params.Services,
		Location:      params.Location,
		ActivatedAt:   params.ActivatedAt,
	}
	if err := s.db.QueryRowContext(
		ctx,
		insertBusinessSQL,
		params.ID,
		params.TenantID,
		string(params.Status),
		params.Name,
		params.Website,
		params.Aliases,
		params.Category,
		string(params.Practitioners),
		string(params.Services),
		jsonbArg(params.Location),
		params.ActivatedAt,
	).Scan(&business.CreatedAt); err != nil {
		return Business{}, fmt.Errorf("insert business: %w", err)
	}
	return business, nil
}

// GetBusiness loads a business scoped to tenantID (AUTH-3 ownership validation,
// Setup read). A missing or cross-tenant business returns ErrNotFound.
func (s *BusinessStore) GetBusiness(ctx context.Context, tenantID, businessID domain.ID) (Business, error) {
	if s == nil || s.db == nil {
		return Business{}, errors.New("business store database is required")
	}

	business, err := scanBusiness(s.db.QueryRowContext(ctx, getBusinessSQL, businessID, tenantID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Business{}, ErrNotFound
		}
		return Business{}, fmt.Errorf("get business: %w", err)
	}
	return business, nil
}

// ListBusinesses returns the tenant's businesses, oldest first (GET /me / SPA
// bootstrap). Scoped by the tenant_id column.
func (s *BusinessStore) ListBusinesses(ctx context.Context, tenantID domain.ID) ([]Business, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("business store database is required")
	}

	rows, err := s.db.QueryContext(ctx, listBusinessesSQL, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list businesses: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	businesses := []Business{}
	for rows.Next() {
		business, err := scanBusiness(rows)
		if err != nil {
			return nil, err
		}
		businesses = append(businesses, business)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate businesses: %w", err)
	}
	return businesses, nil
}

// ResolveTenantID returns only the business's tenant id. It is the sole
// tenant-unscoped business lookup in the package: the store-layer analogue of
// session->tenant resolution, used once by callers without ambient tenant
// context (Temporal activities via LoadRunSpec, CLI) to bootstrap the tenant
// before every subsequent call uses the normal tenant-checked methods
// (design 02). A missing business returns ErrNotFound.
func (s *BusinessStore) ResolveTenantID(ctx context.Context, businessID domain.ID) (domain.ID, error) {
	if s == nil || s.db == nil {
		return uuid.Nil, errors.New("business store database is required")
	}

	var tenantID domain.ID
	if err := s.db.QueryRowContext(ctx, resolveTenantIDSQL, businessID).Scan(&tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("resolve tenant id: %w", err)
	}
	return tenantID, nil
}

func scanBusiness(row rowScanner) (Business, error) {
	var business Business
	var aliases stringSlice
	if err := row.Scan(
		&business.ID,
		&business.TenantID,
		&business.Status,
		&business.Name,
		&business.Website,
		&aliases,
		&business.Category,
		&business.Practitioners,
		&business.Services,
		nullableJSON{&business.Location},
		&business.CreatedAt,
		&business.ActivatedAt,
	); err != nil {
		return Business{}, err
	}
	business.Aliases = aliases
	return business, nil
}

// nullableJSON scans a nullable jsonb column into a json.RawMessage.
// database/sql maps NULL only into exactly *[]byte, not named byte-slice types
// like json.RawMessage, so nullable jsonb needs this wrapper.
type nullableJSON struct{ dst *json.RawMessage }

func (n nullableJSON) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*n.dst = nil
	case []byte:
		*n.dst = append(json.RawMessage(nil), v...)
	case string:
		*n.dst = json.RawMessage(v)
	default:
		return fmt.Errorf("unsupported source type %T for jsonb", src)
	}
	return nil
}

// stringSlice scans a JSON array text (e.g. to_jsonb(text[])) into []string.
type stringSlice []string

func (s *stringSlice) Scan(src any) error {
	if src == nil {
		*s = nil
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("unsupported source type %T for string array", src)
	}
	return json.Unmarshal(raw, (*[]string)(s))
}

func normalizeCreateBusinessParams(params CreateBusinessParams) (CreateBusinessParams, error) {
	if params.ID == uuid.Nil {
		id, err := domain.NewID()
		if err != nil {
			return CreateBusinessParams{}, err
		}
		params.ID = id
	}
	if err := validateUUIDv7("business id", params.ID); err != nil {
		return CreateBusinessParams{}, err
	}
	if err := validateUUIDv7("tenant id", params.TenantID); err != nil {
		return CreateBusinessParams{}, err
	}
	if strings.TrimSpace(params.Name) == "" {
		return CreateBusinessParams{}, errors.New("business name is required")
	}
	if params.Status != BusinessStatusDraft && params.Status != BusinessStatusActive {
		return CreateBusinessParams{}, fmt.Errorf("business status must be %q or %q", BusinessStatusDraft, BusinessStatusActive)
	}
	if params.Aliases == nil {
		params.Aliases = []string{}
	}
	if len(params.Practitioners) == 0 {
		params.Practitioners = json.RawMessage("[]")
	}
	if len(params.Services) == 0 {
		params.Services = json.RawMessage("[]")
	}
	return params, nil
}

// jsonbArg passes a nullable jsonb column: an empty payload becomes SQL NULL,
// otherwise the raw JSON text (cast to jsonb in the statement).
func jsonbArg(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}
