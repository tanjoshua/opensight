package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"opensight/internal/billing"
	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAccountMembershipsSupportMultipleAccountsAndPreserveAnOwner(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)
	s := New(db)

	a, err := s.CreateOperatorAccount(ctx, CreateOperatorAccountParams{Name: "Membership Account A"})
	if err != nil {
		t.Fatalf("create account A: %v", err)
	}
	b, err := s.CreateOperatorAccount(ctx, CreateOperatorAccountParams{Name: "Membership Account B"})
	if err != nil {
		t.Fatalf("create account B: %v", err)
	}
	var userIDs []domain.ID
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = ANY($1)", []domain.ID{a.ID, b.ID})
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = ANY($1)", []domain.ID{a.ID, b.ID})
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE id = ANY($1)", userIDs)
	})

	ownerA, err := s.AddAccountMember(ctx, AddAccountMemberParams{AccountID: a.ID, Email: "multi@example.com", Role: AccountRoleOwner})
	if err != nil {
		t.Fatalf("add account A owner: %v", err)
	}
	userIDs = append(userIDs, ownerA.UserID)
	viewerB, err := s.AddAccountMember(ctx, AddAccountMemberParams{AccountID: b.ID, Email: "multi@example.com", Role: AccountRoleViewer})
	if err != nil {
		t.Fatalf("add account B viewer: %v", err)
	}
	if viewerB.UserID != ownerA.UserID {
		t.Fatalf("global user ids differ: %s vs %s", ownerA.UserID, viewerB.UserID)
	}
	memberships, err := s.ListAccountMemberships(ctx, ownerA.UserID)
	if err != nil {
		t.Fatalf("list memberships: %v", err)
	}
	if len(memberships) != 2 {
		t.Fatalf("memberships = %d, want 2", len(memberships))
	}

	owner2, err := s.AddAccountMember(ctx, AddAccountMemberParams{AccountID: a.ID, Email: "second-owner@example.com", Role: AccountRoleOwner})
	if err != nil {
		t.Fatalf("add second owner: %v", err)
	}
	userIDs = append(userIDs, owner2.UserID)
	ids := []domain.ID{ownerA.UserID, owner2.UserID}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(userID domain.ID) {
			defer wg.Done()
			errs <- s.UpdateAccountMemberRole(ctx, a.ID, userID, AccountRoleMember)
		}(id)
	}
	wg.Wait()
	close(errs)
	var succeeded, rejected int
	for err := range errs {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrLastOwner) {
			rejected++
		} else {
			t.Fatalf("unexpected demotion error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent demotions succeeded/rejected = %d/%d, want 1/1", succeeded, rejected)
	}
}

func TestAccountStoreCreateOperatorAccountAndUser(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	admin := New(db)
	account, err := admin.CreateOperatorAccount(ctx, CreateOperatorAccountParams{Name: "  Admin Account  "})
	if err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE id IN (SELECT user_id FROM account_memberships WHERE account_id = $1)", account.ID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", account.ID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	if account.Name != "Admin Account" {
		t.Fatalf("account name = %q, want trimmed name", account.Name)
	}

	// CreateOperatorAccount inserts the subscription row in the same transaction: a
	// CLI-provisioned account is comped on the starter plan (design 08 "Operator
	// comps").
	var planCode string
	var comped bool
	if err := db.QueryRow(ctx, "SELECT plan_code, comped FROM subscriptions WHERE account_id = $1", account.ID).Scan(&planCode, &comped); err != nil {
		t.Fatalf("load account subscription: %v", err)
	}
	if planCode != billing.Starter.Code {
		t.Fatalf("plan code = %q, want %q", planCode, billing.Starter.Code)
	}
	if !comped {
		t.Fatal("comped = false, want true for a CLI-provisioned account")
	}

	// AddAccountMember leaves google_sub unset: the operator provisions the row, and
	// the user's first Google sign-in links it (design 07 "Auth and
	// accounts").
	member, err := admin.AddAccountMember(ctx, AddAccountMemberParams{
		AccountID: account.ID,
		Email:     "  Owner@Example.com  ",
		Role:      AccountRoleOwner,
	})
	if err != nil {
		t.Fatalf("AddAccountMember: %v", err)
	}

	if member.AccountID != account.ID {
		t.Fatalf("membership account id = %s, want %s", member.AccountID, account.ID)
	}
	if member.Email != "owner@example.com" {
		t.Fatalf("user email = %q, want lowercase trimmed email", member.Email)
	}

	var googleSub *string
	if err := db.QueryRow(ctx, "SELECT google_sub FROM users WHERE id = $1", member.UserID).Scan(&googleSub); err != nil {
		t.Fatalf("load user google_sub: %v", err)
	}
	if googleSub != nil {
		t.Fatalf("google_sub = %v, want NULL until the first Google sign-in", *googleSub)
	}

	// SubscriptionStore.GetByAccount plus the catalog resolves the entitlements
	// the RUN-5 schedule derives its interval from.
	subscriptions := New(db)
	sub, err := subscriptions.GetByAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetByAccount: %v", err)
	}
	plan, err := billing.PlanFor(sub.PlanCode)
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	if plan.Code != billing.Starter.Code {
		t.Fatalf("plan code = %q, want %q", plan.Code, billing.Starter.Code)
	}
	if plan.RunInterval != "weekly" {
		t.Fatalf("run interval = %q, want weekly", plan.RunInterval)
	}
	if plan.PromptLimit != 20 {
		t.Fatalf("prompt limit = %d, want 20", plan.PromptLimit)
	}
}

// TestAccountStoreCreateAccount is the BILL-3 atomicity + shape acceptance
// test: CreateAccount yields a account named from the email local part, an
// uncomped starter subscription, a user, and no business row.
func TestAccountStoreCreateAccount(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	accounts := New(db)

	account, user, err := accounts.CreateAccount(ctx, CreateAccountParams{
		Email:     "  Founder@Example.com  ",
		GoogleSub: "google-sub-founder",
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE id IN (SELECT user_id FROM account_memberships WHERE account_id = $1)", account.ID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", account.ID)
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE account_id = $1", account.ID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	if account.Name != "founder" {
		t.Fatalf("account name = %q, want email local part %q", account.Name, "founder")
	}
	if user.Email != "founder@example.com" {
		t.Fatalf("user email = %q, want lowercase trimmed email", user.Email)
	}
	memberships, err := accounts.ListAccountMemberships(ctx, user.ID)
	if err != nil || len(memberships) != 1 || memberships[0].AccountID != account.ID {
		t.Fatalf("owner memberships = %+v, %v", memberships, err)
	}

	var planCode string
	var comped bool
	var stripeCustomerID, stripeSubscriptionID, stripeStatus *string
	if err := db.QueryRow(ctx, `
		SELECT plan_code, comped, stripe_customer_id, stripe_subscription_id, stripe_status FROM subscriptions WHERE account_id = $1`, account.ID).Scan(&planCode, &comped, &stripeCustomerID, &stripeSubscriptionID, &stripeStatus); err != nil {
		t.Fatalf("load account subscription: %v", err)
	}
	if planCode != billing.Starter.Code {
		t.Fatalf("plan code = %q, want %q", planCode, billing.Starter.Code)
	}
	if comped {
		t.Fatal("comped = true, want false for a self-serve signup")
	}
	if stripeCustomerID != nil || stripeSubscriptionID != nil || stripeStatus != nil {
		t.Fatalf("stripe columns not all null: customer=%v subscription=%v status=%v", stripeCustomerID, stripeSubscriptionID, stripeStatus)
	}

	businesses := New(db)
	list, err := businesses.ListBusinesses(ctx, account.ID)
	if err != nil {
		t.Fatalf("ListBusinesses: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("businesses = %d, want 0 (account survives with no business)", len(list))
	}
}

// TestAccountStoreCreateAccountDuplicateEmailRollsBack is the BILL-3
// atomicity acceptance test: a duplicate-email signup fails with
// ErrEmailTaken and leaves no orphan account or subscription behind.
func TestAccountStoreCreateAccountDuplicateEmailRollsBack(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	accounts := New(db)

	account, _, err := accounts.CreateAccount(ctx, CreateAccountParams{
		Email:     "duplicate@example.com",
		GoogleSub: "google-sub-duplicate-1",
	})
	if err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE id IN (SELECT user_id FROM account_memberships WHERE account_id = $1)", account.ID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", account.ID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	var accountsBefore int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM accounts").Scan(&accountsBefore); err != nil {
		t.Fatalf("count accounts before: %v", err)
	}

	_, _, err = accounts.CreateAccount(ctx, CreateAccountParams{
		Email:     "Duplicate@Example.com",
		GoogleSub: "google-sub-duplicate-2",
	})
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("second CreateAccount error = %v, want ErrEmailTaken", err)
	}

	var accountsAfter int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM accounts").Scan(&accountsAfter); err != nil {
		t.Fatalf("count accounts after: %v", err)
	}
	if accountsAfter != accountsBefore {
		t.Fatalf("account count changed from %d to %d; duplicate signup left an orphan account", accountsBefore, accountsAfter)
	}
}
