package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAuthStore exercises the AUTH-1 identity and session repository against
// a real Postgres: identity lookup by google_sub and by email (incl. citext
// case-insensitivity and ErrNotFound), session create/get with the
// user+account join, expiry handling, login-time purge of expired rows,
// idempotent delete, and FK cascade on user delete.
func TestAuthStore(t *testing.T) {
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

	accountID := mustNewID(t)
	userID := mustNewID(t)
	const email = "Owner@Example.com" // mixed case; citext lookup must match
	const googleSub = "google-sub-owner"

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", userID)
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})

	insertAccount(t, db, ctx, accountID, "Auth Account")
	if _, err := db.Exec(ctx, "INSERT INTO users (id, email, google_sub) VALUES ($1, $2, $3)", userID, email, googleSub); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO account_memberships (account_id, user_id, role) VALUES ($1, $2, 'owner')", accountID, userID); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	auth := New(db)

	// --- GetUserByGoogleSub: exact match on the OIDC subject claim. ---
	bySub, err := auth.GetUserByGoogleSub(ctx, googleSub)
	if err != nil {
		t.Fatalf("GetUserByGoogleSub: %v", err)
	}
	if bySub.UserID != userID {
		t.Fatalf("user id = %s, want %s", bySub.UserID, userID)
	}
	if _, err := auth.GetUserByGoogleSub(ctx, "unknown-sub"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserByGoogleSub(unknown) err = %v, want ErrNotFound", err)
	}

	// --- GetUserByEmail: citext case-insensitive + join to account. ---
	byEmail, err := auth.GetUserByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail (lowercased): %v", err)
	}
	if byEmail.UserID != userID {
		t.Fatalf("user id = %s, want %s", byEmail.UserID, userID)
	}
	if byEmail.GoogleSub == nil || *byEmail.GoogleSub != googleSub {
		t.Fatalf("google_sub = %v, want %q", byEmail.GoogleSub, googleSub)
	}
	if _, err := auth.GetUserByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserByEmail(unknown) err = %v, want ErrNotFound", err)
	}

	// --- CreateSession + GetSession (join user+account). ---
	liveHash := tokenHashFor("live-token")
	if err := auth.CreateSession(ctx, CreateSessionParams{
		TokenHash: liveHash,
		UserID:    userID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	su, err := auth.GetSession(ctx, liveHash)
	if err != nil {
		t.Fatalf("GetSession(live): %v", err)
	}
	if su.UserID != userID || su.Email != email {
		t.Fatalf("session user = %+v, want user %s / %s", su, userID, email)
	}

	// --- Expired session resolves as ErrNotFound. ---
	expiredHash := tokenHashFor("expired-token")
	if _, err := db.Exec(ctx, "INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() - interval '1 minute')", expiredHash, userID); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}
	if _, err := auth.GetSession(ctx, expiredHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession(expired) err = %v, want ErrNotFound", err)
	}

	// --- Login-time purge: CreateSession deletes this user's expired rows. ---
	if err := auth.CreateSession(ctx, CreateSessionParams{
		TokenHash: tokenHashFor("second-live"),
		UserID:    userID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("second CreateSession: %v", err)
	}
	var expiredCount int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1 AND expires_at <= now()", userID).Scan(&expiredCount); err != nil {
		t.Fatalf("count expired: %v", err)
	}
	if expiredCount != 0 {
		t.Fatalf("expired sessions after purge = %d, want 0", expiredCount)
	}

	// --- DeleteSession is idempotent. ---
	if err := auth.DeleteSession(ctx, liveHash); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := auth.GetSession(ctx, liveHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession after delete err = %v, want ErrNotFound", err)
	}
	if err := auth.DeleteSession(ctx, liveHash); err != nil {
		t.Fatalf("second DeleteSession (idempotent) err = %v, want nil", err)
	}

	// --- FK cascade: deleting the user removes its remaining sessions. ---
	if _, err := db.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var remaining int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1", userID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining sessions: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("sessions after user delete = %d, want 0 (FK cascade)", remaining)
	}
}

func tokenHashFor(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// TestGetSessionBillingJoin is BILL-6's GetSession integration test: the
// LEFT JOIN to subscriptions returns plan_code and the billing columns
// (comped, Stripe id/status), live off whatever the row currently holds, and
// a account with no subscriptions row surfaces as an explicit error rather
// than the same ErrNotFound an absent/expired session returns.
func TestGetSessionBillingJoin(t *testing.T) {
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

	auth := New(db)
	subs := New(db)

	t.Run("comped starter fixture, then a live Upsert is reflected without a new session", func(t *testing.T) {
		accountID := mustNewID(t)
		userID := mustNewID(t)
		const email = "billing-join@example.com"
		t.Cleanup(func() {
			_, _ = db.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", userID)
			_, _ = db.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
			_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
			_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
		})

		// insertAccount's fixture (account_fixture_test.go) inserts a comped
		// starter subscription — this must keep working unchanged.
		insertAccount(t, db, ctx, accountID, "Billing Join Account")
		if _, err := db.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)", userID, email); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		mustExec(t, db, ctx, "INSERT INTO account_memberships (account_id, user_id, role) VALUES ($1, $2, 'owner')", accountID, userID)

		liveHash := tokenHashFor("billing-join-live")
		if err := auth.CreateSession(ctx, CreateSessionParams{TokenHash: liveHash, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		identitySession, err := auth.GetSession(ctx, liveHash)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		su, err := auth.ResolveAccountSession(ctx, identitySession, accountSlug("Billing Join Account", accountID), false)
		if err != nil {
			t.Fatalf("ResolveAccountSession: %v", err)
		}
		if su.PlanCode != billing.Starter.Code {
			t.Fatalf("PlanCode = %q, want %q", su.PlanCode, billing.Starter.Code)
		}
		if !su.Billing.Comped {
			t.Fatalf("Billing.Comped = false, want true (insertAccount fixture)")
		}

		// Live Upsert to a non-comped active Stripe subscription; GetSession
		// re-derives from the current row on every call, no caching.
		custID, subID, status := "cus_join_test", "sub_join_test", "active"
		if err := subs.Upsert(ctx, UpsertSubscriptionParams{
			AccountID: accountID, PlanCode: billing.Starter.Code,
			StripeCustomerID: &custID, StripeSubscriptionID: &subID, StripeStatus: &status,
			Comped: false,
		}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}

		identitySession, err = auth.GetSession(ctx, liveHash)
		if err != nil {
			t.Fatalf("GetSession after upsert: %v", err)
		}
		su2, err := auth.ResolveAccountSession(ctx, identitySession, accountSlug("Billing Join Account", accountID), false)
		if err != nil {
			t.Fatalf("ResolveAccountSession after upsert: %v", err)
		}
		if su2.Billing.Comped {
			t.Fatalf("Billing.Comped = true after upsert to comped=false")
		}
		if su2.Billing.StripeSubscriptionID != subID {
			t.Fatalf("Billing.StripeSubscriptionID = %q, want %q", su2.Billing.StripeSubscriptionID, subID)
		}
		if su2.Billing.StripeStatus != status {
			t.Fatalf("Billing.StripeStatus = %q, want %q", su2.Billing.StripeStatus, status)
		}
	})

	t.Run("account with no subscriptions row returns an explicit error, not ErrNotFound", func(t *testing.T) {
		accountID := mustNewID(t)
		userID := mustNewID(t)
		const email = "no-sub@example.com"
		t.Cleanup(func() {
			_, _ = db.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", userID)
			_, _ = db.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
			_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
		})

		// A bare account insert (not insertAccount, which also inserts the
		// subscription row) is the fixture for a account with no subscriptions
		// row at all — structurally impossible via signup, but reachable if a
		// account somehow predates the backfill.
		if _, err := db.Exec(ctx, "INSERT INTO accounts (id, name, slug) VALUES ($1, $2, $3)", accountID, "No Subscription Account", "test-"+accountID.String()); err != nil {
			t.Fatalf("insert account: %v", err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO users (id, email) VALUES ($1, $2)", userID, email); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		mustExec(t, db, ctx, "INSERT INTO account_memberships (account_id, user_id, role) VALUES ($1, $2, 'owner')", accountID, userID)

		liveHash := tokenHashFor("no-sub-live")
		if err := auth.CreateSession(ctx, CreateSessionParams{TokenHash: liveHash, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		identitySession, err := auth.GetSession(ctx, liveHash)
		if err != nil {
			t.Fatalf("GetSession identity: %v", err)
		}
		_, err = auth.ResolveAccountSession(ctx, identitySession, "test-"+accountID.String(), false)
		if err == nil {
			t.Fatal("GetSession with no subscriptions row: want an error, got nil")
		}
		if errors.Is(err, ErrNotFound) {
			t.Fatalf("GetSession err = %v, want an explicit non-ErrNotFound error (not masquerading as an absent session)", err)
		}
	})
}

// TestAuthStoreLinksGoogleSubByEmail exercises the identity-resolution ladder
// the Google callback relies on (google_auth.go resolveOrCreateGoogleUser):
// an operator-created row (opensight user create) has no google_sub until its
// first sign-in links it by email, after which it resolves by sub too.
func TestAuthStoreLinksGoogleSubByEmail(t *testing.T) {
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

	accountID := mustNewID(t)
	userID := mustNewID(t)
	const email = "linked@example.com"
	const sub = "sub-linked"
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = $1", accountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})
	insertAccount(t, db, ctx, accountID, "Link Account")

	auth := New(db)
	member, err := auth.AddAccountMember(ctx, AddAccountMemberParams{UserID: userID, AccountID: accountID, Email: email, Role: AccountRoleOwner})
	if err != nil {
		t.Fatalf("AddAccountMember: %v", err)
	}

	// Before the first Google sign-in: found by email, no google_sub yet.
	beforeLink, err := auth.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if beforeLink.GoogleSub != nil {
		t.Fatalf("google_sub = %v, want nil before the first sign-in", *beforeLink.GoogleSub)
	}
	if _, err := auth.GetUserByGoogleSub(ctx, sub); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserByGoogleSub before linking err = %v, want ErrNotFound", err)
	}

	if err := auth.SetUserGoogleSub(ctx, member.UserID, sub); err != nil {
		t.Fatalf("SetUserGoogleSub: %v", err)
	}

	// After linking: resolves by sub, and the email lookup now carries it.
	bySub, err := auth.GetUserByGoogleSub(ctx, sub)
	if err != nil {
		t.Fatalf("GetUserByGoogleSub after linking: %v", err)
	}
	if bySub.UserID != member.UserID {
		t.Fatalf("GetUserByGoogleSub user id = %s, want %s", bySub.UserID, member.UserID)
	}
	afterLink, err := auth.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail after linking: %v", err)
	}
	if afterLink.GoogleSub == nil || *afterLink.GoogleSub != sub {
		t.Fatalf("google_sub after linking = %v, want %q", afterLink.GoogleSub, sub)
	}
}

// TestPlatformOwnerSeesEveryAccount covers the anyAccount branch that lets
// the platform owner (api.isPlatformOwner) list and open workspaces they are
// not a member of. The membership rule itself is the security boundary, so
// both sides of the flag are asserted.
func TestPlatformOwnerSeesEveryAccount(t *testing.T) {
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

	store := New(db)
	ownID, otherID := mustNewID(t), mustNewID(t)
	userID := mustNewID(t)
	const email = "platform-owner@example.com"
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", userID)
		_, _ = db.Exec(ctx, "DELETE FROM account_memberships WHERE user_id = $1", userID)
		_, _ = db.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id = ANY($1)", []domain.ID{ownID, otherID})
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = ANY($1)", []domain.ID{ownID, otherID})
	})

	insertAccount(t, db, ctx, ownID, "Owner Member Account")
	insertAccount(t, db, ctx, otherID, "Someone Elses Account")
	mustExec(t, db, ctx, "INSERT INTO users (id, email) VALUES ($1, $2)", userID, email)
	mustExec(t, db, ctx, "INSERT INTO account_memberships (account_id, user_id, role) VALUES ($1, $2, 'admin')", ownID, userID)

	hash := tokenHashFor("platform-owner-live")
	if err := store.CreateSession(ctx, CreateSessionParams{TokenHash: hash, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	session, err := store.GetSession(ctx, hash)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	otherSlug := accountSlug("Someone Elses Account", otherID)

	t.Run("ListAllAccounts carries real roles and owner for non-members", func(t *testing.T) {
		all, err := store.ListAllAccounts(ctx, userID)
		if err != nil {
			t.Fatalf("ListAllAccounts: %v", err)
		}
		roles := map[domain.ID]AccountRole{}
		for _, m := range all {
			roles[m.AccountID] = m.Role
		}
		if roles[ownID] != AccountRoleAdmin {
			t.Errorf("role on own account = %q, want %q", roles[ownID], AccountRoleAdmin)
		}
		if roles[otherID] != AccountRoleOwner {
			t.Errorf("role on non-member account = %q, want %q", roles[otherID], AccountRoleOwner)
		}

		mine, err := store.ListAccountMemberships(ctx, userID)
		if err != nil {
			t.Fatalf("ListAccountMemberships: %v", err)
		}
		if len(mine) != 1 || mine[0].AccountID != ownID {
			t.Fatalf("ListAccountMemberships returned %d rows, want only the membership", len(mine))
		}
	})

	t.Run("a non-member account opens only with anyAccount", func(t *testing.T) {
		if _, err := store.ResolveAccountSession(ctx, session, otherSlug, false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ResolveAccountSession(anyAccount=false) err = %v, want ErrNotFound", err)
		}
		su, err := store.ResolveAccountSession(ctx, session, otherSlug, true)
		if err != nil {
			t.Fatalf("ResolveAccountSession(anyAccount=true): %v", err)
		}
		if su.AccountID != otherID || su.Role != AccountRoleOwner {
			t.Fatalf("resolved account/role = %v/%q, want %v/%q", su.AccountID, su.Role, otherID, AccountRoleOwner)
		}
	})

	t.Run("anyAccount does not inflate a real membership role", func(t *testing.T) {
		su, err := store.ResolveAccountSession(ctx, session, accountSlug("Owner Member Account", ownID), true)
		if err != nil {
			t.Fatalf("ResolveAccountSession: %v", err)
		}
		if su.Role != AccountRoleAdmin {
			t.Fatalf("Role = %q, want %q", su.Role, AccountRoleAdmin)
		}
	})
}
