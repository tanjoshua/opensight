package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opensight/internal/auth"
	"opensight/internal/billing"
	"opensight/internal/domain"
	"opensight/internal/store"

	"github.com/google/uuid"
)

// fakeAuthStore is an in-memory authStore for handler tests. It records how many
// argon2id verifies the login path would burn indirectly via the credential
// lookups it returns.
type fakeAuthStore struct {
	credsByEmail map[string]store.UserCredentials
	credsErr     error
	sessions     map[string]store.SessionUser // keyed by string(tokenHash)
	created      []store.CreateSessionParams
	deleted      [][]byte
}

func (f *fakeAuthStore) GetUserCredentials(_ context.Context, email string) (store.UserCredentials, error) {
	if f.credsErr != nil {
		return store.UserCredentials{}, f.credsErr
	}
	c, ok := f.credsByEmail[strings.ToLower(email)]
	if !ok {
		return store.UserCredentials{}, store.ErrNotFound
	}
	return c, nil
}

func (f *fakeAuthStore) CreateSession(_ context.Context, params store.CreateSessionParams) error {
	f.created = append(f.created, params)
	if f.sessions == nil {
		f.sessions = map[string]store.SessionUser{}
	}
	su := store.SessionUser{
		UserID:    params.UserID,
		ExpiresAt: params.ExpiresAt,
		// Real GetSession LEFT JOINs subscriptions (BILL-6); mirror that here
		// with comped-starter, the same fixture insertTenant uses for store
		// integration tests, so existing handler tests keep passing unchanged.
		PlanCode: billing.Starter.Code,
		Billing:  billing.State{Comped: true},
	}
	// The real store's session lookup always joins to users/tenants, so mirror
	// that here: find the matching credentials entry by user id and populate
	// Email/TenantID/TenantName too.
	for _, creds := range f.credsByEmail {
		if creds.UserID == params.UserID {
			su.Email = creds.Email
			su.TenantID = creds.TenantID
			su.TenantName = creds.TenantName
			break
		}
	}
	f.sessions[string(params.TokenHash)] = su
	return nil
}

func (f *fakeAuthStore) GetSession(_ context.Context, tokenHash []byte) (store.SessionUser, error) {
	su, ok := f.sessions[string(tokenHash)]
	if !ok {
		return store.SessionUser{}, store.ErrNotFound
	}
	return su, nil
}

func (f *fakeAuthStore) DeleteSession(_ context.Context, tokenHash []byte) error {
	f.deleted = append(f.deleted, tokenHash)
	delete(f.sessions, string(tokenHash))
	return nil
}

// fakeAccountStore is an in-memory accountStore for handler tests. It writes
// into the same fakeAuthStore.credsByEmail the login path reads, so Signup and
// Login share state the way they share a database.
type fakeAccountStore struct {
	auth *fakeAuthStore
	err  error
}

func (f *fakeAccountStore) CreateAccount(_ context.Context, params store.CreateAccountParams) (store.Tenant, store.User, error) {
	if f.err != nil {
		return store.Tenant{}, store.User{}, f.err
	}

	email := strings.ToLower(strings.TrimSpace(params.Email))
	if f.auth.credsByEmail == nil {
		f.auth.credsByEmail = map[string]store.UserCredentials{}
	}
	if _, exists := f.auth.credsByEmail[email]; exists {
		return store.Tenant{}, store.User{}, store.ErrEmailTaken
	}

	userID, err := domain.NewID()
	if err != nil {
		return store.Tenant{}, store.User{}, err
	}
	tenantID, err := domain.NewID()
	if err != nil {
		return store.Tenant{}, store.User{}, err
	}
	local, _, _ := strings.Cut(email, "@")

	passwordHash := params.PasswordHash
	f.auth.credsByEmail[email] = store.UserCredentials{
		UserID:       userID,
		TenantID:     tenantID,
		Email:        email,
		TenantName:   local,
		PasswordHash: &passwordHash,
	}
	return store.Tenant{ID: tenantID, Name: local}, store.User{ID: userID, TenantID: tenantID, Email: email}, nil
}

// fakeBusinessStore is an in-memory businessStore for handler tests.
type fakeBusinessStore struct {
	businesses []store.Business
	business   store.Business // returned by GetBusiness/CreateBusiness when no error
	err        error
	getErr     error
	createErr  error
	created    []store.CreateBusinessParams
	updated    []store.UpdateBusinessProfileParams
	updateErr  error
	gotTenant  domain.ID
}

func (f *fakeBusinessStore) ListBusinesses(_ context.Context, tenantID domain.ID) ([]store.Business, error) {
	f.gotTenant = tenantID
	return f.businesses, f.err
}

func (f *fakeBusinessStore) GetBusiness(_ context.Context, _, _ domain.ID) (store.Business, error) {
	if f.getErr != nil {
		return store.Business{}, f.getErr
	}
	return f.business, nil
}

func (f *fakeBusinessStore) CreateBusiness(_ context.Context, params store.CreateBusinessParams) (store.Business, error) {
	f.created = append(f.created, params)
	if f.createErr != nil {
		return store.Business{}, f.createErr
	}
	b := f.business
	if params.ID != uuid.Nil {
		b.ID = params.ID
	}
	b.TenantID = params.TenantID
	b.Status = params.Status
	b.Name = params.Name
	b.Website = params.Website
	return b, nil
}

func (f *fakeBusinessStore) UpdateActiveProfile(_ context.Context, params store.UpdateBusinessProfileParams) (store.Business, error) {
	f.updated = append(f.updated, params)
	if f.updateErr != nil {
		return store.Business{}, f.updateErr
	}
	b := f.business
	if params.Name != nil {
		b.Name = *params.Name
	}
	if params.WebsiteSet {
		b.Website = params.Website
	}
	if params.Aliases != nil {
		b.Aliases = *params.Aliases
	}
	if params.Category != nil {
		b.Category = params.Category
	}
	if params.Services != nil {
		b.Services = *params.Services
	}
	if params.Location != nil {
		b.Location = *params.Location
	}
	f.business = b
	return b, nil
}

func newTestServer(f *fakeAuthStore) *Server {
	return &Server{
		auth:          f,
		accounts:      &fakeAccountStore{auth: f},
		businesses:    &fakeBusinessStore{},
		subscriptions: &fakeSubscriptionStore{sub: store.Subscription{PlanCode: billing.Starter.Code}},
		// classActive's representative gate call (CreateBusiness) reaches
		// startGeneration, which needs a temporal client even for the gate
		// tests that only exercise the interceptor.
		temporal:      &fakeTemporalClient{},
		secureCookies: false,
		sessionTTL:    time.Hour,
	}
}

func mustHashV7(t *testing.T, raw string) domain.ID {
	t.Helper()
	id, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("parse id %q: %v", raw, err)
	}
	return id
}

func realHash(t *testing.T, password string) string {
	t.Helper()
	h, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return h
}

const (
	userID   = "01950000-0000-7000-8000-0000000000a1"
	tenantID = "01950000-0000-7000-8000-0000000000b2"
)

func loginFixture(t *testing.T, password string) *fakeAuthStore {
	return &fakeAuthStore{
		credsByEmail: map[string]store.UserCredentials{
			"user@example.com": {
				UserID:       mustHashV7(t, userID),
				TenantID:     mustHashV7(t, tenantID),
				Email:        "user@example.com",
				TenantName:   "Acme Clinic",
				PasswordHash: ptrString(realHash(t, password)),
			},
		},
	}
}

func ptrString(s string) *string { return &s }

func TestHealthzStillOK(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !bytes.Equal(body, []byte("ok\n")) {
		t.Fatalf("healthz body = %q, want %q", body, "ok\n")
	}
}

func TestSPAFallbackServesEmbeddedIndex(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})
	req := httptest.NewRequest(http.MethodGet, "/responses", nil)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("SPA fallback status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<title>OpenSight</title>") {
		t.Fatalf("SPA fallback body did not contain embedded index: %q", rec.Body.String())
	}
}

func TestStaticAssetRoutesDoNotFallBackToSPA(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})

	index := httptest.NewRecorder()
	srv.Routes().ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if index.Code != http.StatusOK {
		t.Fatalf("index asset status = %d, want 200", index.Code)
	}

	missingAsset := httptest.NewRecorder()
	srv.Routes().ServeHTTP(missingAsset, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if missingAsset.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", missingAsset.Code)
	}
	if strings.Contains(missingAsset.Body.String(), "<title>OpenSight</title>") {
		t.Fatalf("missing asset fell back to index.html: %q", missingAsset.Body.String())
	}
}

// TestRouterMethodNotAllowedUsesProblemJSON pins the one remaining non-RPC
// error path: a route that exists but was hit with the wrong method (chi's
// router-level MethodNotAllowed, wired to writeProblem in Routes()). Unknown
// paths falling back to the SPA shell are covered by
// TestSPAFallbackServesEmbeddedIndex; /rpc's own 404 behavior for unknown
// procedures is covered by TestRPCRequiresConnectProtocolHeader (rpc_test.go).
func TestRouterMethodNotAllowedUsesProblemJSON(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})

	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"title":"method not allowed"`) {
		t.Fatalf("problem body %q does not contain the expected title", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":405`) {
		t.Fatalf("problem body %q does not contain status 405", rec.Body.String())
	}
}
