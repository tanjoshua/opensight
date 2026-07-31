package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"opensight/internal/billing"
	testdb "opensight/internal/store/testdb"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAuthStore exercises the AUTH-1 credential and session repository against a
// real Postgres: credential lookup (incl. citext case-insensitivity and
// ErrNotFound), session create/get with the user+tenant join, expiry handling,
// login-time purge of expired rows, idempotent delete, and FK cascade on user
// delete.
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

	tenantID := mustNewID(t)
	userID := mustNewID(t)
	const email = "Owner@Example.com" // mixed case; citext lookup must match
	const passwordHash = "$argon2id$v=19$m=19456,t=2,p=1$ClzmGysxMTp/RFyIazZhUQ$AG2OnfvJYMcvJEC7hyKJpMH8ZCwby9D+K/Mzqb5imbg"

	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query111, userID)
		_, _ = testdb.Exec(ctx, db, testdb.Query112, userID)
		_, _ = testdb.Exec(ctx, db, testdb.Query113, tenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query114, tenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Auth Tenant")
	if _, err := testdb.Exec(ctx, db, testdb.Query115, userID, tenantID, email, passwordHash); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	auth := NewAuthStore(db)

	// --- GetUserCredentials: citext case-insensitive + join to tenant. ---
	creds, err := auth.GetUserCredentials(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("GetUserCredentials (lowercased): %v", err)
	}
	if creds.UserID != userID || creds.TenantID != tenantID {
		t.Fatalf("creds ids = %s/%s, want %s/%s", creds.UserID, creds.TenantID, userID, tenantID)
	}
	if creds.TenantName != "Auth Tenant" {
		t.Fatalf("creds tenant name = %q, want Auth Tenant", creds.TenantName)
	}
	if creds.PasswordHash == nil || *creds.PasswordHash != passwordHash {
		t.Fatalf("creds password hash = %v, want %q", creds.PasswordHash, passwordHash)
	}
	if _, err := auth.GetUserCredentials(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserCredentials(unknown) err = %v, want ErrNotFound", err)
	}

	// --- CreateSession + GetSession (join user+tenant). ---
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
	if su.UserID != userID || su.Email != email || su.TenantName != "Auth Tenant" {
		t.Fatalf("session user = %+v, want user %s / %s / Auth Tenant", su, userID, email)
	}

	// --- Expired session resolves as ErrNotFound. ---
	expiredHash := tokenHashFor("expired-token")
	if _, err := testdb.Exec(ctx, db, testdb.Query116, expiredHash, userID); err != nil {
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
	if err := testdb.QueryRow(ctx, db, testdb.Query117, userID).Scan(&expiredCount); err != nil {
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
	if _, err := testdb.Exec(ctx, db, testdb.Query118, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var remaining int
	if err := testdb.QueryRow(ctx, db, testdb.Query119, userID).Scan(&remaining); err != nil {
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
// a tenant with no subscriptions row surfaces as an explicit error rather
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

	auth := NewAuthStore(db)
	subs := NewSubscriptionStore(db)

	t.Run("comped starter fixture, then a live Upsert is reflected without a new session", func(t *testing.T) {
		tenantID := mustNewID(t)
		userID := mustNewID(t)
		const email = "billing-join@example.com"
		t.Cleanup(func() {
			_, _ = testdb.Exec(ctx, db, testdb.Query111, userID)
			_, _ = testdb.Exec(ctx, db, testdb.Query112, userID)
			_, _ = testdb.Exec(ctx, db, testdb.Query113, tenantID)
			_, _ = testdb.Exec(ctx, db, testdb.Query114, tenantID)
		})

		// insertTenant's fixture (tenant_fixture_test.go) inserts a comped
		// starter subscription — this must keep working unchanged.
		insertTenant(t, db, ctx, tenantID, "Billing Join Tenant")
		if _, err := testdb.Exec(ctx, db, testdb.Query115, userID, tenantID, email, ""); err != nil {
			t.Fatalf("insert user: %v", err)
		}

		liveHash := tokenHashFor("billing-join-live")
		if err := auth.CreateSession(ctx, CreateSessionParams{TokenHash: liveHash, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		su, err := auth.GetSession(ctx, liveHash)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		if su.PlanCode != billing.Starter.Code {
			t.Fatalf("PlanCode = %q, want %q", su.PlanCode, billing.Starter.Code)
		}
		if !su.Billing.Comped {
			t.Fatalf("Billing.Comped = false, want true (insertTenant fixture)")
		}

		// Live Upsert to a non-comped active Stripe subscription; GetSession
		// re-derives from the current row on every call, no caching.
		custID, subID, status := "cus_join_test", "sub_join_test", "active"
		if err := subs.Upsert(ctx, UpsertSubscriptionParams{
			TenantID: tenantID, PlanCode: billing.Starter.Code,
			StripeCustomerID: &custID, StripeSubscriptionID: &subID, StripeStatus: &status,
			Comped: false,
		}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}

		su2, err := auth.GetSession(ctx, liveHash)
		if err != nil {
			t.Fatalf("GetSession after upsert: %v", err)
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

	t.Run("tenant with no subscriptions row returns an explicit error, not ErrNotFound", func(t *testing.T) {
		tenantID := mustNewID(t)
		userID := mustNewID(t)
		const email = "no-sub@example.com"
		t.Cleanup(func() {
			_, _ = testdb.Exec(ctx, db, testdb.Query111, userID)
			_, _ = testdb.Exec(ctx, db, testdb.Query112, userID)
			_, _ = testdb.Exec(ctx, db, testdb.Query114, tenantID)
		})

		// TestQuery225 alone (not insertTenant, which also inserts Query226's
		// subscription row) is the fixture for a tenant with no subscriptions
		// row at all — structurally impossible via signup, but reachable if a
		// tenant somehow predates the backfill.
		if _, err := testdb.Exec(ctx, db, testdb.Query225, tenantID, "No Subscription Tenant"); err != nil {
			t.Fatalf("insert tenant: %v", err)
		}
		if _, err := testdb.Exec(ctx, db, testdb.Query115, userID, tenantID, email, ""); err != nil {
			t.Fatalf("insert user: %v", err)
		}

		liveHash := tokenHashFor("no-sub-live")
		if err := auth.CreateSession(ctx, CreateSessionParams{TokenHash: liveHash, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		_, err := auth.GetSession(ctx, liveHash)
		if err == nil {
			t.Fatal("GetSession with no subscriptions row: want an error, got nil")
		}
		if errors.Is(err, ErrNotFound) {
			t.Fatalf("GetSession err = %v, want an explicit non-ErrNotFound error (not masquerading as an absent session)", err)
		}
	})
}
