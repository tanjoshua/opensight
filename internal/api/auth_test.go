package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"opensight/internal/auth"
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
	f.sessions[string(params.TokenHash)] = store.SessionUser{
		UserID:    params.UserID,
		ExpiresAt: params.ExpiresAt,
	}
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

// fakeBusinessStore is an in-memory businessStore for handler tests.
type fakeBusinessStore struct {
	businesses []store.Business
	err        error
	getErr     error
	gotTenant  domain.ID
}

func (f *fakeBusinessStore) ListBusinesses(_ context.Context, tenantID domain.ID) ([]store.Business, error) {
	f.gotTenant = tenantID
	return f.businesses, f.err
}

func (f *fakeBusinessStore) GetBusiness(_ context.Context, _, _ domain.ID) (store.Business, error) {
	return store.Business{}, f.getErr
}

func newTestServer(f *fakeAuthStore) *Server {
	return &Server{auth: f, businesses: &fakeBusinessStore{}, secureCookies: false, sessionTTL: time.Hour}
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

func doJSON(t *testing.T, srv *Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return doJSONWithRequestedWith(t, srv, method, path, body, cookie, true)
}

func doJSONWithoutRequestedWith(t *testing.T, srv *Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return doJSONWithRequestedWith(t, srv, method, path, body, cookie, false)
}

func doJSONWithRequestedWith(t *testing.T, srv *Server, method, path, body string, cookie *http.Cookie, requestedWith bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if requestedWith && method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

func findCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
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

func TestLoginSuccessSetsCookieAndReturnsMeShape(t *testing.T) {
	f := loginFixture(t, "s3cret-passphrase")
	srv := newTestServer(f)

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"user@example.com","password":"s3cret-passphrase"}`, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}

	cookie := findCookie(rec.Result(), sessionCookieName)
	if cookie == nil {
		t.Fatal("no session cookie set on login")
	}
	if !cookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Secure {
		t.Error("session cookie is Secure in dev (secureCookies=false); want not Secure")
	}
	if cookie.Value == "" {
		t.Error("session cookie value is empty")
	}
	if len(f.created) != 1 {
		t.Fatalf("CreateSession called %d times, want 1", len(f.created))
	}

	want := `{"user":{"id":"` + userID + `","email":"user@example.com"},"tenant":{"id":"` + tenantID + `","name":"Acme Clinic"}}` + "\n"
	if rec.Body.String() != want {
		t.Fatalf("login body = %q, want %q", rec.Body.String(), want)
	}
}

func TestLoginSecureCookieWhenSecureFlag(t *testing.T) {
	f := loginFixture(t, "pw")
	srv := &Server{auth: f, secureCookies: true, sessionTTL: time.Hour}
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"user@example.com","password":"pw"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	cookie := findCookie(rec.Result(), sessionCookieName)
	if cookie == nil || !cookie.Secure {
		t.Fatal("session cookie is not Secure when secureCookies=true")
	}
}

// TestLoginFailuresAreByteIdentical is the acceptance-critical uniformity test:
// unknown email, a user with no password_hash, and a wrong password must all
// return the exact same 401 bytes and Content-Type.
func TestLoginFailuresAreByteIdentical(t *testing.T) {
	f := &fakeAuthStore{
		credsByEmail: map[string]store.UserCredentials{
			"real@example.com": {
				UserID:       mustHashV7(t, userID),
				TenantID:     mustHashV7(t, tenantID),
				Email:        "real@example.com",
				TenantName:   "Acme",
				PasswordHash: ptrString(realHash(t, "the-right-password")),
			},
			"nopass@example.com": {
				UserID:       mustHashV7(t, userID),
				TenantID:     mustHashV7(t, tenantID),
				Email:        "nopass@example.com",
				TenantName:   "Acme",
				PasswordHash: nil, // predates AUTH-2
			},
			"bad-hash@example.com": {
				UserID:       mustHashV7(t, userID),
				TenantID:     mustHashV7(t, tenantID),
				Email:        "bad-hash@example.com",
				TenantName:   "Acme",
				PasswordHash: ptrString("not-a-phc-hash"),
			},
		},
	}
	srv := newTestServer(f)

	unknown := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"ghost@example.com","password":"whatever"}`, nil)
	noHash := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"nopass@example.com","password":"whatever"}`, nil)
	badHash := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"bad-hash@example.com","password":"whatever"}`, nil)
	wrongPw := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"real@example.com","password":"wrong-password"}`, nil)

	for _, rec := range []*httptest.ResponseRecorder{unknown, noHash, badHash, wrongPw} {
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("failure content-type = %q, want application/problem+json", ct)
		}
		if findCookie(rec.Result(), sessionCookieName) != nil {
			t.Fatal("a failed login set a session cookie")
		}
	}

	if unknown.Body.String() != wrongPw.Body.String() {
		t.Fatalf("unknown-email body %q != wrong-password body %q", unknown.Body.String(), wrongPw.Body.String())
	}
	if noHash.Body.String() != wrongPw.Body.String() {
		t.Fatalf("no-hash body %q != wrong-password body %q", noHash.Body.String(), wrongPw.Body.String())
	}
	if badHash.Body.String() != wrongPw.Body.String() {
		t.Fatalf("bad-hash body %q != wrong-password body %q", badHash.Body.String(), wrongPw.Body.String())
	}
	if len(f.created) != 0 {
		t.Fatalf("failed logins created %d sessions, want 0", len(f.created))
	}
}

func TestLoginMalformedJSON(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/login", `{not json`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
}

func TestLoginBlankFields(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"  ","password":""}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestStateChangingRoutesRequireRequestedWith(t *testing.T) {
	f := loginFixture(t, "pw")
	srv := newTestServer(f)

	loginWithoutHeader := doJSONWithoutRequestedWith(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"user@example.com","password":"pw"}`, nil)
	if loginWithoutHeader.Code != http.StatusForbidden {
		t.Fatalf("login without X-Requested-With status = %d, want 403", loginWithoutHeader.Code)
	}
	if ct := loginWithoutHeader.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}

	login := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"user@example.com","password":"pw"}`, nil)
	cookie := findCookie(login.Result(), sessionCookieName)
	if cookie == nil {
		t.Fatal("login did not set a cookie")
	}

	logoutWithoutHeader := doJSONWithoutRequestedWith(t, srv, http.MethodPost, "/api/v1/logout", "", cookie)
	if logoutWithoutHeader.Code != http.StatusForbidden {
		t.Fatalf("logout without X-Requested-With status = %d, want 403", logoutWithoutHeader.Code)
	}
}

func TestRequireRequestedWithMiddlewareIsMethodAware(t *testing.T) {
	called := 0
	next := requireRequestedWith(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/future", nil)
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without header status = %d, want 403", rec.Code)
	}
	if called != 0 {
		t.Fatalf("handler called %d times for rejected POST, want 0", called)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/future", nil)
	rec = httptest.NewRecorder()
	next.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("GET without header status = %d, want 204", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/v1/future", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec = httptest.NewRecorder()
	next.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH with header status = %d, want 204", rec.Code)
	}
}

func TestLogoutDeletesSessionAndClearsCookie(t *testing.T) {
	f := loginFixture(t, "pw")
	srv := newTestServer(f)

	// Establish a session.
	login := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"user@example.com","password":"pw"}`, nil)
	cookie := findCookie(login.Result(), sessionCookieName)
	if cookie == nil {
		t.Fatal("login did not set a cookie")
	}

	out := doJSON(t, srv, http.MethodPost, "/api/v1/logout", "", cookie)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", out.Code)
	}
	cleared := findCookie(out.Result(), sessionCookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("logout did not clear the cookie: %+v", cleared)
	}
	if len(f.deleted) != 1 {
		t.Fatalf("DeleteSession called %d times, want 1", len(f.deleted))
	}
}

func TestLogoutRequiresSession(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/logout", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-cookie logout status = %d, want 401", rec.Code)
	}
}

func TestMeUnauthorizedWithoutSession(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/me", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me status = %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
}

func TestMeUnauthorizedClearsDeadCookie(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})
	dead := &http.Cookie{Name: sessionCookieName, Value: "nonexistent-token"}
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/me", "", dead)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me status = %d, want 401", rec.Code)
	}
	cleared := findCookie(rec.Result(), sessionCookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("me did not clear the dead cookie: %+v", cleared)
	}
}

func TestMeSuccess(t *testing.T) {
	f := loginFixture(t, "pw")
	srv := newTestServer(f)
	srv.businesses = &fakeBusinessStore{businesses: []store.Business{{
		ID:     mustHashV7(t, businessIDForTest),
		Name:   "Acme Clinic",
		Status: store.BusinessStatusActive,
	}}}

	login := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"user@example.com","password":"pw"}`, nil)
	cookie := findCookie(login.Result(), sessionCookieName)
	if cookie == nil {
		t.Fatal("login did not set a cookie")
	}
	// The fake stores the session keyed by hash but without user email/tenant;
	// populate what GetSession should return for this token.
	su := f.sessions[string(hashSessionToken(cookie.Value))]
	su.Email = "user@example.com"
	su.TenantName = "Acme Clinic"
	su.TenantID = mustHashV7(t, tenantID)
	f.sessions[string(hashSessionToken(cookie.Value))] = su

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/me", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := `{"user":{"id":"` + userID + `","email":"user@example.com"},"tenant":{"id":"` + tenantID + `","name":"Acme Clinic"},"businesses":[{"id":"` + businessIDForTest + `","name":"Acme Clinic","status":"active"}]}` + "\n"
	if rec.Body.String() != want {
		t.Fatalf("me body = %q, want %q", rec.Body.String(), want)
	}
}

func TestLoginStoreErrorIsInternal(t *testing.T) {
	f := &fakeAuthStore{credsErr: errors.New("db down")}
	srv := newTestServer(f)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/login",
		`{"email":"user@example.com","password":"pw"}`, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

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

func TestRouterErrorsUseProblemJSON(t *testing.T) {
	srv := newTestServer(&fakeAuthStore{})

	cases := []struct {
		name   string
		method string
		path   string
		status int
		title  string
		detail string
	}{
		{
			name:   "not found",
			method: http.MethodGet,
			path:   "/api/v1/nope",
			status: http.StatusNotFound,
			title:  "not found",
			detail: "not found",
		},
		{
			name:   "method not allowed",
			method: http.MethodGet,
			path:   "/api/v1/login",
			status: http.StatusMethodNotAllowed,
			title:  "method not allowed",
			detail: "method not allowed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Fatalf("content-type = %q, want application/problem+json", ct)
			}

			if !strings.Contains(rec.Body.String(), `"title":"`+tc.title+`"`) {
				t.Fatalf("problem body %q does not contain title %q", rec.Body.String(), tc.title)
			}
			if !strings.Contains(rec.Body.String(), `"detail":"`+tc.detail+`"`) {
				t.Fatalf("problem body %q does not contain detail %q", rec.Body.String(), tc.detail)
			}
			if !strings.Contains(rec.Body.String(), `"status":`+strconv.Itoa(tc.status)) {
				t.Fatalf("problem body %q does not contain status %d", rec.Body.String(), tc.status)
			}
		})
	}
}
