package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/llm"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeGoogleAuth is the googleAuthenticator test double: it echoes state into
// the (fake) consent URL so the test can recover it from the redirect, and
// Exchange returns identity/err as configured, standing in for Google's token
// endpoint without ever leaving the process.
type fakeGoogleAuth struct {
	identity googleIdentity
	err      error
}

func (f *fakeGoogleAuth) AuthCodeURL(state, _ string) string {
	return "https://accounts.google.com/o/oauth2/v2/auth?state=" + url.QueryEscape(state)
}

func (f *fakeGoogleAuth) Exchange(context.Context, string, string) (googleIdentity, error) {
	return f.identity, f.err
}

var _ googleAuthenticator = (*fakeGoogleAuth)(nil)

// errUnreachedExchange fails a test loudly if Exchange is ever called: it's
// configured on fakeGoogleAuth for cases the callback must reject before
// reaching the exchange (e.g. a state mismatch).
var errUnreachedExchange = errors.New("google auth: Exchange should not have been called")

// noRedirectClient returns each 3xx response directly instead of following
// it, so a test can inspect Location and Set-Cookie — while still running
// every response through jar, exactly like a browser would.
func noRedirectClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// startGoogleSignIn drives GET /auth/google/start and returns the state
// query param Google would echo back to the callback.
func startGoogleSignIn(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	resp, err := client.Get(baseURL + "/auth/google/start")
	if err != nil {
		t.Fatalf("GET /auth/google/start: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/auth/google/start status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("/auth/google/start Location: %v", err)
	}
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("/auth/google/start did not carry a state param through to the consent URL")
	}
	return state
}

func openAPITestDB(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run API integration tests")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)
	return db, ctx
}

// TestRPCSessionLifecycleAgainstPostgres drives the whole sign-in path
// through real HTTP against Postgres: GET /auth/google/start (state + PKCE
// cookie, redirect to the consent screen) → GET /auth/google/callback (account
// provisioned, session cookie set) → GetMe → Logout → GetMe.
func TestRPCSessionLifecycleAgainstPostgres(t *testing.T) {
	db, ctx := openAPITestDB(t)
	repository := store.New(db)
	email := "api-" + mustDomainID(t).String() + "@example.com"
	googleAuth := &fakeGoogleAuth{identity: googleIdentity{Sub: "sub-" + mustDomainID(t).String(), Email: email, EmailVerified: true}}
	srv := New(Deps{Store: repository, AppBaseURL: "https://app.example.com", GoogleAuth: googleAuth})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	browser := noRedirectClient(jar)

	state := startGoogleSignIn(t, browser, ts.URL)

	callbackResp, err := browser.Get(ts.URL + "/auth/google/callback?code=fake-code&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("GET /auth/google/callback: %v", err)
	}
	_ = callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusFound {
		t.Fatalf("/auth/google/callback status = %d, want %d", callbackResp.StatusCode, http.StatusFound)
	}
	if loc, err := callbackResp.Location(); err != nil || loc.Path != "/accounts" {
		t.Fatalf("/auth/google/callback redirected to %v (err=%v), want /accounts", loc, err)
	}

	rpcClient := opensightv1connect.NewAuthServiceClient(browser, ts.URL+"/rpc")

	me, err := rpcClient.GetMe(ctx, connect.NewRequest(&opensightv1.GetMeRequest{}))
	if err != nil {
		t.Fatalf("GetMe after sign-in: %v", err)
	}
	if len(me.Msg.GetMemberships()) != 1 || me.Msg.GetMemberships()[0].GetAccount() == nil {
		t.Fatalf("GetMe memberships = %+v, want one owner membership", me.Msg.GetMemberships())
	}
	membership := me.Msg.GetMemberships()[0]
	accountID, err := uuid.Parse(membership.GetAccount().GetId())
	if err != nil {
		t.Fatalf("parse account id: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID) })
	if me.Msg.GetUser().GetEmail() != email || membership.GetRole() != opensightv1.AccountRole_ACCOUNT_ROLE_OWNER {
		t.Fatalf("GetMe = %+v, want signed-up user with owner membership", me.Msg)
	}

	accountClient := opensightv1connect.NewAccountServiceClient(browser, ts.URL+"/rpc")
	accountReq := connect.NewRequest(&opensightv1.GetAccountContextRequest{AccountSlug: membership.GetAccount().GetSlug()})
	accountReq.Header().Set("X-OpenSight-Account-Slug", membership.GetAccount().GetSlug())
	account, err := accountClient.GetAccountContext(ctx, accountReq)
	if err != nil {
		t.Fatalf("GetAccountContext after sign-in: %v", err)
	}
	if account.Msg.GetPlan().GetCode() != billing.Starter.Code || account.Msg.GetAccess() != opensightv1.Access_ACCESS_NEVER {
		t.Fatalf("GetAccountContext = %+v, want unpaid Starter account", account.Msg)
	}

	if _, err := rpcClient.Logout(ctx, connect.NewRequest(&opensightv1.LogoutRequest{})); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := rpcClient.GetMe(ctx, connect.NewRequest(&opensightv1.GetMeRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("GetMe after logout code = %v, want Unauthenticated", connect.CodeOf(err))
	}
}

// TestGoogleCallbackStateMismatchRedirectsToLoginError covers the CSRF guard:
// a callback whose state doesn't match the one minted by /auth/google/start
// (or has no oauth cookie at all) must never proceed to Exchange — it
// redirects straight to the login page's error state.
func TestGoogleCallbackStateMismatchRedirectsToLoginError(t *testing.T) {
	db, _ := openAPITestDB(t)
	repository := store.New(db)
	googleAuth := &fakeGoogleAuth{err: errUnreachedExchange}
	srv := New(Deps{Store: repository, AppBaseURL: "https://app.example.com", GoogleAuth: googleAuth})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	browser := noRedirectClient(jar)

	startGoogleSignIn(t, browser, ts.URL) // sets the oauth cookie; state discarded

	resp, err := browser.Get(ts.URL + "/auth/google/callback?code=fake-code&state=not-the-real-state")
	if err != nil {
		t.Fatalf("GET /auth/google/callback: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("callback Location: %v", err)
	}
	if loc.Path != "/login" || loc.Query().Get("error") != "google" {
		t.Fatalf("callback redirected to %v, want /login?error=google", loc)
	}
}

func TestBusinessRPCUsesConcreteStore(t *testing.T) {
	db, ctx := openAPITestDB(t)
	accountID := mustDomainID(t)
	otherAccountID := mustDomainID(t)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = ANY($1)", []domain.ID{accountID, otherAccountID})
	})
	for id, name := range map[domain.ID]string{accountID: "API Account", otherAccountID: "Other Account"} {
		if _, err := db.Exec(ctx, "INSERT INTO accounts (id, name, slug) VALUES ($1, $2, $3)", id, name, "test-"+id.String()); err != nil {
			t.Fatalf("insert account: %v", err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO subscriptions (account_id, plan_code, comped) VALUES ($1, $2, true)", id, billing.Starter.Code); err != nil {
			t.Fatalf("insert subscription: %v", err)
		}
	}

	jobClient := &fakeJobClient{}
	repository := store.New(db)
	srv := New(Deps{Store: repository, Jobs: jobClient, Limiter: llm.NewLimiter(2)})
	session := withSessionUser(ctx, store.SessionUser{
		UserID: mustDomainID(t), AccountID: accountID, Email: "api@example.com",
		AccountName: "API Account", ExpiresAt: time.Now().Add(time.Hour),
		PlanCode: billing.Starter.Code, Billing: billing.State{Comped: true},
	})

	created, err := srv.CreateBusiness(session, connect.NewRequest(&opensightv1.CreateBusinessRequest{Name: "Atlas Dental"}))
	if err != nil {
		t.Fatalf("CreateBusiness: %v", err)
	}
	if len(jobClient.inserted) != 1 {
		t.Fatalf("inserted jobs = %d, want one", len(jobClient.inserted))
	}
	businessID, err := uuid.Parse(created.Msg.GetBusiness().GetId())
	if err != nil {
		t.Fatalf("parse business id: %v", err)
	}
	got, err := repository.GetBusiness(ctx, accountID, businessID)
	if err != nil || got.Name != "Atlas Dental" || got.Status != store.BusinessStatusDraft {
		t.Fatalf("stored business = %+v, err=%v", got, err)
	}

	website := "https://atlas.example"
	if _, err := srv.RegenerateProposal(session, connect.NewRequest(&opensightv1.RegenerateProposalRequest{
		BusinessId: businessID.String(),
		Website:    &website,
	})); err != nil {
		t.Fatalf("RegenerateProposal with website: %v", err)
	}
	got, err = repository.GetBusiness(ctx, accountID, businessID)
	if err != nil || got.Website == nil || *got.Website != website {
		t.Fatalf("business after website correction = %+v, err=%v", got, err)
	}
	if len(jobClient.cancelled) != 1 || len(jobClient.inserted) != 2 {
		t.Fatalf("generation replacements: cancelled=%v inserted=%d", jobClient.cancelled, len(jobClient.inserted))
	}

	otherSession := withSessionUser(ctx, store.SessionUser{AccountID: otherAccountID, PlanCode: billing.Starter.Code})
	_, err = srv.GetBusiness(otherSession, connect.NewRequest(&opensightv1.GetBusinessRequest{BusinessId: businessID.String()}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-account GetBusiness code = %v, want NotFound", connect.CodeOf(err))
	}
}

// TestGenerateQuestionsAgainstPostgres covers GenerateQuestions' three guards
// against a real business row (design 03): draft-only, server-side profile
// validation, and the plan's prompt_limit — never a client-supplied count —
// deciding how many questions come back.
func TestGenerateQuestionsAgainstPostgres(t *testing.T) {
	db, ctx := openAPITestDB(t)
	accountID := mustDomainID(t)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
	})
	if _, err := db.Exec(ctx, "INSERT INTO accounts (id, name, slug) VALUES ($1, $2, $3)", accountID, "Questions Account", "test-"+accountID.String()); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO subscriptions (account_id, plan_code, comped) VALUES ($1, $2, true)", accountID, billing.Starter.Code); err != nil {
		t.Fatalf("insert subscription: %v", err)
	}

	questions, err := llm.NewStubQuestionsRunner()
	if err != nil {
		t.Fatalf("NewStubQuestionsRunner: %v", err)
	}
	repository := store.New(db)
	srv := New(Deps{Store: repository, Jobs: &fakeJobClient{}, Limiter: llm.NewLimiter(2), Questions: questions})
	session := withSessionUser(ctx, store.SessionUser{
		UserID: mustDomainID(t), AccountID: accountID, Email: "questions@example.com",
		AccountName: "Questions Account", ExpiresAt: time.Now().Add(time.Hour),
		PlanCode: billing.Starter.Code, Billing: billing.State{Comped: true},
	})

	created, err := srv.CreateBusiness(session, connect.NewRequest(&opensightv1.CreateBusinessRequest{Name: "Atlas Dental"}))
	if err != nil {
		t.Fatalf("CreateBusiness: %v", err)
	}
	businessID := created.Msg.GetBusiness().GetId()
	businessUUID, err := uuid.Parse(businessID)
	if err != nil {
		t.Fatalf("parse business id: %v", err)
	}

	validProfile := &opensightv1.ProposedProfile{
		Name:     "Atlas Dental",
		Category: "dental clinic",
		Services: []string{"root canal"},
		Location: &opensightv1.Location{Country: "SG"},
	}

	// An invalid profile (missing category) never reaches the runner.
	_, err = srv.GenerateQuestions(session, connect.NewRequest(&opensightv1.GenerateQuestionsRequest{
		BusinessId: businessID,
		Profile:    &opensightv1.ProposedProfile{Name: "Atlas Dental", Location: &opensightv1.Location{Country: "SG"}},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid profile code = %v, want InvalidArgument", connect.CodeOf(err))
	}

	// The happy path returns exactly the plan's prompt_limit questions — a
	// count the client never supplies.
	resp, err := srv.GenerateQuestions(session, connect.NewRequest(&opensightv1.GenerateQuestionsRequest{
		BusinessId: businessID,
		Profile:    validProfile,
	}))
	if err != nil {
		t.Fatalf("GenerateQuestions: %v", err)
	}
	if len(resp.Msg.GetPrompts()) != billing.Starter.PromptLimit {
		t.Fatalf("prompts = %d, want %d (billing.Starter.PromptLimit)", len(resp.Msg.GetPrompts()), billing.Starter.PromptLimit)
	}

	// An activated business refuses GenerateQuestions: it is a draft-only RPC.
	// Activation is forced directly rather than through ApplyProposal, whose
	// River schedule/run side effects are out of scope for this test.
	if _, err := db.Exec(ctx,
		"UPDATE businesses SET status = 'active', activated_at = now(), category = 'dental clinic', location = '{\"country\":\"SG\"}'::jsonb WHERE id = $1",
		businessUUID); err != nil {
		t.Fatalf("force-activate business: %v", err)
	}
	_, err = srv.GenerateQuestions(session, connect.NewRequest(&opensightv1.GenerateQuestionsRequest{
		BusinessId: businessID,
		Profile:    validProfile,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("post-activation GenerateQuestions code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

// TestDeleteAccountAgainstPostgres covers workspace deletion end to end: the
// live-subscription guard, the retyped-name guard, and the cascade itself —
// the account row plus every table that hangs off it. The cascade is the
// whole point of migration 00024, and only a real database can show it.
func TestDeleteAccountAgainstPostgres(t *testing.T) {
	db, ctx := openAPITestDB(t)
	accountID := mustDomainID(t)
	t.Cleanup(func() { _, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id = $1", accountID) })
	if _, err := db.Exec(ctx, "INSERT INTO accounts (id, name, slug) VALUES ($1, $2, $3)", accountID, "Doomed Workspace", "test-"+accountID.String()); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := db.Exec(ctx,
		"INSERT INTO subscriptions (account_id, plan_code, comped, stripe_subscription_id, stripe_status) VALUES ($1, $2, false, 'sub_live', 'active')",
		accountID, billing.Starter.Code); err != nil {
		t.Fatalf("insert subscription: %v", err)
	}
	businessID := mustDomainID(t)
	if _, err := db.Exec(ctx,
		"INSERT INTO businesses (id, account_id, status, name) VALUES ($1, $2, 'draft', 'Atlas Dental')",
		businessID, accountID); err != nil {
		t.Fatalf("insert business: %v", err)
	}

	repository := store.New(db)
	srv := New(Deps{Store: repository})
	session := withSessionUser(ctx, store.SessionUser{
		UserID: mustDomainID(t), AccountID: accountID, Email: "admin@example.com",
		AccountName: "Doomed Workspace", Role: store.AccountRoleAdmin,
		ExpiresAt: time.Now().Add(time.Hour), PlanCode: billing.Starter.Code,
	})
	deleteReq := func(name string) *connect.Request[opensightv1.DeleteAccountRequest] {
		return connect.NewRequest(&opensightv1.DeleteAccountRequest{ConfirmName: name})
	}

	// A live Stripe subscription blocks deletion regardless of confirmation:
	// deleting the row would not stop Stripe billing the customer.
	if _, err := srv.DeleteAccount(session, deleteReq("Doomed Workspace")); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("DeleteAccount with a live subscription code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
	if _, err := db.Exec(ctx, "UPDATE subscriptions SET stripe_status = 'canceled' WHERE account_id = $1", accountID); err != nil {
		t.Fatalf("cancel subscription: %v", err)
	}

	if _, err := srv.DeleteAccount(session, deleteReq("not the name")); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("DeleteAccount with a mismatched name code = %v, want InvalidArgument", connect.CodeOf(err))
	}

	// Case and surrounding whitespace are forgiven; the name is not.
	if _, err := srv.DeleteAccount(session, deleteReq("  doomed workspace ")); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	for _, q := range []string{
		"SELECT count(*) FROM accounts WHERE id = $1",
		"SELECT count(*) FROM subscriptions WHERE account_id = $1",
		"SELECT count(*) FROM businesses WHERE account_id = $1",
	} {
		var n int
		if err := db.QueryRow(ctx, q, accountID).Scan(&n); err != nil {
			t.Fatalf("count after delete (%s): %v", q, err)
		}
		if n != 0 {
			t.Fatalf("rows remaining after delete (%s) = %d, want 0", q, n)
		}
	}
	if _, err := srv.DeleteAccount(session, deleteReq("Doomed Workspace")); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("DeleteAccount on a deleted workspace code = %v, want NotFound", connect.CodeOf(err))
	}
}
