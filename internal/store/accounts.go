package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"opensight/internal/billing"
	"opensight/internal/domain"
	storesqlc "opensight/internal/store/sqlc"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Account struct {
	ID        domain.ID
	Name      string
	Slug      string
	CreatedAt time.Time
}

type User struct {
	ID        domain.ID
	Email     string
	GoogleSub *string
	CreatedAt time.Time
}

type AccountRole string

const (
	AccountRoleOwner  AccountRole = "owner"
	AccountRoleAdmin  AccountRole = "admin"
	AccountRoleMember AccountRole = "member"
	AccountRoleViewer AccountRole = "viewer"
)

func (r AccountRole) Valid() bool {
	switch r {
	case AccountRoleOwner, AccountRoleAdmin, AccountRoleMember, AccountRoleViewer:
		return true
	default:
		return false
	}
}

type AccountMembership struct {
	AccountID domain.ID
	UserID    domain.ID
	Email     string
	Role      AccountRole
	CreatedAt time.Time
	Pending   bool
	Account   Account
}

var (
	ErrEmailTaken = errors.New("email already registered")
	ErrLastOwner  = errors.New("an account must have at least one owner")
)

type CreateOperatorAccountParams struct {
	ID   domain.ID
	Name string
}

func (s *Store) CreateOperatorAccount(ctx context.Context, params CreateOperatorAccountParams) (Account, error) {
	account, err := normalizeAccount(params.ID, params.Name)
	if err != nil {
		return Account{}, err
	}
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		created, err := q.InsertAccount(ctx, storesqlc.InsertAccountParams{ID: account.ID, Name: account.Name, Slug: account.Slug})
		if err != nil {
			return fmt.Errorf("insert account: %w", err)
		}
		account.CreatedAt = created
		return CreateSubscriptionInTx(ctx, q, account.ID, billing.Starter.Code, true)
	})
	return account, err
}

type AddAccountMemberParams struct {
	AccountID domain.ID
	UserID    domain.ID
	Email     string
	Role      AccountRole
}

func (s *Store) AddAccountMember(ctx context.Context, params AddAccountMemberParams) (AccountMembership, error) {
	if err := validateUUIDv7("account id", params.AccountID); err != nil {
		return AccountMembership{}, err
	}
	params.Email = strings.ToLower(strings.TrimSpace(params.Email))
	if !validEmail(params.Email) {
		return AccountMembership{}, errors.New("a valid email is required")
	}
	if !params.Role.Valid() {
		return AccountMembership{}, errors.New("a valid account role is required")
	}
	if params.UserID == uuid.Nil {
		var err error
		params.UserID, err = domain.NewID()
		if err != nil {
			return AccountMembership{}, err
		}
	}
	if err := validateUUIDv7("user id", params.UserID); err != nil {
		return AccountMembership{}, err
	}

	member := AccountMembership{AccountID: params.AccountID, Email: params.Email}
	err := s.withTx(ctx, func(q *storesqlc.Queries) error {
		u, err := q.UpsertUserByEmail(ctx, storesqlc.UpsertUserByEmailParams{ID: params.UserID, Email: params.Email})
		if err != nil {
			return fmt.Errorf("upsert member identity: %w", err)
		}
		member.UserID, member.Pending = u.ID, u.GoogleSub == nil
		m, err := q.UpsertAccountMembership(ctx, storesqlc.UpsertAccountMembershipParams{
			AccountID: params.AccountID, UserID: u.ID, Role: string(params.Role),
		})
		if err != nil {
			return fmt.Errorf("add account membership: %w", err)
		}
		member.Role, member.CreatedAt = AccountRole(m.Role), m.CreatedAt
		return nil
	})
	return member, err
}

// CreateAccountParams provisions the first unpaid account for a new Google
// identity. AccountID is retained as the field name until all callers migrate.
type CreateAccountParams struct {
	AccountID domain.ID
	UserID    domain.ID
	Email     string
	GoogleSub string
}

func (s *Store) CreateAccount(ctx context.Context, params CreateAccountParams) (Account, User, error) {
	email := strings.ToLower(strings.TrimSpace(params.Email))
	local, _, ok := strings.Cut(email, "@")
	if !ok || local == "" || strings.TrimSpace(params.GoogleSub) == "" {
		return Account{}, User{}, errors.New("valid email and google sub are required")
	}
	account, err := normalizeAccount(params.AccountID, local)
	if err != nil {
		return Account{}, User{}, err
	}
	if params.UserID == uuid.Nil {
		params.UserID, err = domain.NewID()
		if err != nil {
			return Account{}, User{}, err
		}
	}
	if err := validateUUIDv7("user id", params.UserID); err != nil {
		return Account{}, User{}, err
	}
	user := User{ID: params.UserID, Email: email, GoogleSub: &params.GoogleSub}
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		account.CreatedAt, err = q.InsertAccount(ctx, storesqlc.InsertAccountParams{ID: account.ID, Name: account.Name, Slug: account.Slug})
		if err != nil {
			return fmt.Errorf("insert account: %w", err)
		}
		if err := CreateSubscriptionInTx(ctx, q, account.ID, billing.Starter.Code, false); err != nil {
			return err
		}
		user.CreatedAt, err = q.InsertUser(ctx, storesqlc.InsertUserParams{ID: user.ID, Email: user.Email, GoogleSub: user.GoogleSub})
		if err != nil {
			if isUniqueViolation(err) {
				return ErrEmailTaken
			}
			return fmt.Errorf("insert user: %w", err)
		}
		_, err = q.InsertAccountMembership(ctx, storesqlc.InsertAccountMembershipParams{AccountID: account.ID, UserID: user.ID, Role: string(AccountRoleOwner)})
		return err
	})
	if err != nil {
		return Account{}, User{}, err
	}
	return account, user, nil
}

type CreateNamedAccountParams struct {
	UserID domain.ID
	Name   string
	Comped bool
}

func (s *Store) CreateNamedAccount(ctx context.Context, params CreateNamedAccountParams) (AccountMembership, error) {
	account, err := normalizeAccount(uuid.Nil, params.Name)
	if err != nil {
		return AccountMembership{}, err
	}
	var member AccountMembership
	err = s.withTx(ctx, func(q *storesqlc.Queries) error {
		account.CreatedAt, err = q.InsertAccount(ctx, storesqlc.InsertAccountParams{ID: account.ID, Name: account.Name, Slug: account.Slug})
		if err != nil {
			return fmt.Errorf("insert account: %w", err)
		}
		if err := CreateSubscriptionInTx(ctx, q, account.ID, billing.Starter.Code, params.Comped); err != nil {
			return err
		}
		created, err := q.InsertAccountMembership(ctx, storesqlc.InsertAccountMembershipParams{AccountID: account.ID, UserID: params.UserID, Role: string(AccountRoleOwner)})
		if err != nil {
			return fmt.Errorf("insert owner membership: %w", err)
		}
		member = AccountMembership{AccountID: account.ID, UserID: params.UserID, Role: AccountRoleOwner, CreatedAt: created, Account: account}
		return nil
	})
	return member, err
}

func normalizeAccount(id domain.ID, name string) (Account, error) {
	var err error
	if id == uuid.Nil {
		id, err = domain.NewID()
		if err != nil {
			return Account{}, err
		}
	}
	if err := validateUUIDv7("account id", id); err != nil {
		return Account{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Account{}, errors.New("account name is required")
	}
	return Account{ID: id, Name: name, Slug: accountSlug(name, id)}, nil
}

func accountSlug(name string, id domain.ID) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r <= unicode.MaxASCII && ((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "account"
	}
	compact := strings.ReplaceAll(id.String(), "-", "")
	return base + "-" + compact[len(compact)-8:]
}

func validEmail(email string) bool {
	local, domainPart, ok := strings.Cut(email, "@")
	return ok && local != "" && domainPart != ""
}

func (s *Store) ListAccountMemberships(ctx context.Context, userID domain.ID) ([]AccountMembership, error) {
	rows, err := s.q(ctx).ListAccountMembershipsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list account memberships: %w", err)
	}
	out := make([]AccountMembership, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountMembership{AccountID: r.AccountID, UserID: userID, Role: AccountRole(r.Role), CreatedAt: r.CreatedAt, Account: Account{ID: r.AccountID, Name: r.Name, Slug: r.Slug}})
	}
	return out, nil
}

// ListAllAccounts returns every account on the platform as a membership
// list, carrying the user's real role where they have one and
// AccountRoleOwner elsewhere. Only the platform owner is routed here
// (api.isPlatformOwner); ListAccountMemberships is the list for everyone
// else.
func (s *Store) ListAllAccounts(ctx context.Context, userID domain.ID) ([]AccountMembership, error) {
	rows, err := s.q(ctx).ListAllAccounts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list all accounts: %w", err)
	}
	out := make([]AccountMembership, 0, len(rows))
	for _, r := range rows {
		m := AccountMembership{AccountID: r.AccountID, UserID: userID, Role: AccountRoleOwner, Account: Account{ID: r.AccountID, Name: r.Name, Slug: r.Slug}}
		if r.Role != nil {
			m.Role = AccountRole(*r.Role)
		}
		if r.CreatedAt != nil {
			m.CreatedAt = *r.CreatedAt
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) GetAccount(ctx context.Context, accountID domain.ID) (Account, error) {
	r, err := s.q(ctx).GetAccountByID(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("get account: %w", err)
	}
	return Account{ID: r.ID, Name: r.Name, Slug: r.Slug, CreatedAt: r.CreatedAt}, nil
}

func (s *Store) ListAccountMembers(ctx context.Context, accountID domain.ID) ([]AccountMembership, error) {
	rows, err := s.q(ctx).ListAccountMembers(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("list account members: %w", err)
	}
	out := make([]AccountMembership, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountMembership{AccountID: accountID, UserID: r.UserID, Email: r.Email, Role: AccountRole(r.Role), CreatedAt: r.CreatedAt, Pending: r.GoogleSub == nil})
	}
	return out, nil
}

func (s *Store) GetAccountMember(ctx context.Context, accountID, userID domain.ID) (AccountMembership, error) {
	r, err := s.q(ctx).GetAccountMembership(ctx, storesqlc.GetAccountMembershipParams{AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountMembership{}, ErrNotFound
	}
	if err != nil {
		return AccountMembership{}, fmt.Errorf("get account membership: %w", err)
	}
	return AccountMembership{AccountID: r.AccountID, UserID: r.UserID, Role: AccountRole(r.Role), CreatedAt: r.CreatedAt}, nil
}

func (s *Store) UpdateAccountMemberRole(ctx context.Context, accountID, userID domain.ID, role AccountRole) error {
	if !role.Valid() {
		return errors.New("a valid account role is required")
	}
	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		if _, err := q.LockAccount(ctx, accountID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		current, err := q.GetAccountMembership(ctx, storesqlc.GetAccountMembershipParams{AccountID: accountID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if AccountRole(current.Role) == AccountRoleOwner && role != AccountRoleOwner {
			count, err := q.CountAccountOwners(ctx, accountID)
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrLastOwner
			}
		}
		n, err := q.UpdateAccountMembershipRole(ctx, storesqlc.UpdateAccountMembershipRoleParams{AccountID: accountID, UserID: userID, Role: string(role)})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// DeleteAccount removes the account and, through the FK cascade every
// account-scoped table carries (migration 00024), its memberships,
// subscription, businesses, and the whole run/result/analysis tree beneath
// them. Irreversible: there is no soft-delete column and nothing restores a
// deleted workspace.
func (s *Store) DeleteAccount(ctx context.Context, accountID domain.ID) error {
	n, err := s.q(ctx).DeleteAccount(ctx, accountID)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RemoveAccountMember(ctx context.Context, accountID, userID domain.ID) error {
	return s.withTx(ctx, func(q *storesqlc.Queries) error {
		if _, err := q.LockAccount(ctx, accountID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		current, err := q.GetAccountMembership(ctx, storesqlc.GetAccountMembershipParams{AccountID: accountID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if AccountRole(current.Role) == AccountRoleOwner {
			count, err := q.CountAccountOwners(ctx, accountID)
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrLastOwner
			}
		}
		n, err := q.DeleteAccountMembership(ctx, storesqlc.DeleteAccountMembershipParams{AccountID: accountID, UserID: userID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
