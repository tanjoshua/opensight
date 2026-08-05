package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BusinessStatus is the persisted lifecycle status for a business.
type BusinessStatus string

const (
	// BusinessStatusDraft is a business still in onboarding, before activation.
	BusinessStatusDraft BusinessStatus = "draft"
	// BusinessStatusActive is an activated business included in monitoring.
	BusinessStatusActive BusinessStatus = "active"
)

// Business is a persisted business row (migration 00003). account_id is the
// tenancy anchor every deeper table scopes through.
type Business struct {
	ID          domain.ID
	AccountID    domain.ID
	Status      BusinessStatus
	Name        string
	Website     *string
	Aliases     []string
	Category    *string
	Services    json.RawMessage
	Location    json.RawMessage
	CreatedAt   time.Time
	ActivatedAt *time.Time
}

// CreateBusinessParams are the inputs for creating a business. AccountID is
// required (account existence is enforced by the FK). If ID is uuid.Nil a UUIDv7
// is generated. Nil Services default to an empty JSON array; nil Aliases default
// to an empty array; Location stays NULL when nil.
type CreateBusinessParams struct {
	ID          domain.ID
	AccountID    domain.ID
	Status      BusinessStatus
	Name        string
	Website     *string
	Aliases     []string
	Category    *string
	Services    json.RawMessage
	Location    json.RawMessage
	ActivatedAt *time.Time
}

type UpdateBusinessProfileParams struct {
	AccountID   domain.ID
	BusinessID domain.ID
	Name       *string
	WebsiteSet bool
	Website    *string
	Aliases    *[]string
	Category   *string
	Services   *json.RawMessage
	Location   *json.RawMessage
}

// CreateBusiness inserts a business owned by params.AccountID. Account existence is enforced by the FK. In
// the same transaction it renames the account to the business name: signup
// seeds accounts.name from the email local part (CreateAccount),
// and onboarding's first business is what replaces that placeholder with the
// real name (design 08 "Signup").
func (s *Store) CreateBusiness(ctx context.Context, params CreateBusinessParams) (Business, error) {

	params, err := normalizeCreateBusinessParams(params)
	if err != nil {
		return Business{}, err
	}

	business := Business{
		ID:          params.ID,
		AccountID:    params.AccountID,
		Status:      params.Status,
		Name:        params.Name,
		Website:     params.Website,
		Aliases:     params.Aliases,
		Category:    params.Category,
		Services:    params.Services,
		Location:    params.Location,
		ActivatedAt: params.ActivatedAt,
	}
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		var location *json.RawMessage
		if len(params.Location) > 0 {
			location = &params.Location
		}
		business.CreatedAt, err = q.InsertBusiness(ctx, storesqlc.InsertBusinessParams{
			ID: params.ID, AccountID: params.AccountID, Status: string(params.Status), Name: params.Name,
			Website: params.Website, Aliases: params.Aliases, Category: params.Category,
			Services: params.Services, Location: location, ActivatedAt: params.ActivatedAt,
		})
		if err != nil {
			return fmt.Errorf("insert business: %w", err)
		}
		if err := q.RenameAccount(ctx, storesqlc.RenameAccountParams{ID: params.AccountID, Name: params.Name}); err != nil {
			return fmt.Errorf("rename account: %w", err)
		}
		return nil
	})
	if err != nil {
		return Business{}, err
	}
	return business, nil
}

// GetBusiness loads a business scoped to accountID — the ownership gate for
// business-scoped reads. A missing or cross-account business returns ErrNotFound.
func (s *Store) GetBusiness(ctx context.Context, accountID, businessID domain.ID) (Business, error) {

	row, err := s.q(ctx).GetBusiness(ctx, storesqlc.GetBusinessParams{ID: businessID, AccountID: accountID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Business{}, ErrNotFound
		}
		return Business{}, fmt.Errorf("get business: %w", err)
	}
	return businessFromSQLC(row), nil
}

func (s *Store) UpdateActiveProfile(ctx context.Context, params UpdateBusinessProfileParams) (Business, error) {
	if err := validateUUIDv7("account id", params.AccountID); err != nil {
		return Business{}, err
	}
	if err := validateUUIDv7("business id", params.BusinessID); err != nil {
		return Business{}, err
	}
	if params.WebsiteSet && params.Website != nil {
		value := strings.TrimSpace(*params.Website)
		if value == "" {
			params.Website = nil
		} else {
			params.Website = &value
		}
	}
	var aliases []string
	if params.Aliases != nil {
		aliases = *params.Aliases
	}
	var name string
	if params.Name != nil {
		name = *params.Name
	}
	var services, location json.RawMessage
	if params.Services != nil {
		services = *params.Services
	}
	if params.Location != nil {
		location = *params.Location
	}
	row, err := s.q(ctx).UpdateActiveBusinessProfile(ctx, storesqlc.UpdateActiveBusinessProfileParams{
		BusinessID: params.BusinessID, AccountID: params.AccountID,
		NameSet: params.Name != nil, Name: name, WebsiteSet: params.WebsiteSet, Website: params.Website,
		AliasesSet: params.Aliases != nil, Aliases: aliases, CategorySet: params.Category != nil,
		Category: params.Category, ServicesSet: params.Services != nil, Services: services,
		LocationSet: params.Location != nil, Location: location,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Business{}, ErrNotFound
		}
		return Business{}, fmt.Errorf("update active business profile: %w", err)
	}
	return businessFromSQLC(row), nil
}

// ListBusinesses returns the account's businesses, oldest first (GET /me / SPA
// bootstrap). Scoped by the account_id column.
func (s *Store) ListBusinesses(ctx context.Context, accountID domain.ID) ([]Business, error) {

	rows, err := s.q(ctx).ListBusinesses(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("list businesses: %w", err)
	}
	businesses := make([]Business, 0, len(rows))
	for _, row := range rows {
		businesses = append(businesses, businessFromSQLC(row))
	}
	return businesses, nil
}

// ResolveAccountID returns only the business's account id. It is the sole
// account-unscoped business lookup in the package: the store-layer analogue of
// session->account resolution, used once by callers without ambient account
// context (Temporal activities via LoadRunSpec, CLI) to bootstrap the account
// before every subsequent call uses the normal account-checked methods
// (design 02). A missing business returns ErrNotFound.
func (s *Store) ResolveAccountID(ctx context.Context, businessID domain.ID) (domain.ID, error) {

	accountID, err := s.q(ctx).ResolveAccountID(ctx, businessID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("resolve account id: %w", err)
	}
	return accountID, nil
}

func businessFromSQLC(row storesqlc.Business) Business {
	var location json.RawMessage
	if row.Location != nil {
		location = *row.Location
	}
	return Business{
		ID: row.ID, AccountID: row.AccountID, Status: BusinessStatus(row.Status), Name: row.Name,
		Website: row.Website, Aliases: row.Aliases, Category: row.Category, Services: row.Services,
		Location: location, CreatedAt: row.CreatedAt, ActivatedAt: row.ActivatedAt,
	}
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
	if err := validateUUIDv7("account id", params.AccountID); err != nil {
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
	if len(params.Services) == 0 {
		params.Services = json.RawMessage("[]")
	}
	return params, nil
}

// jsonbArg passes a nullable jsonb column: an empty payload becomes SQL NULL,
// otherwise the raw JSON text (cast to jsonb in the statement).
