package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"opensight/internal/auth"
	"opensight/internal/store"
)

const sessionCookieName = "opensight_session"

// errNoSession is the sentinel for "no live session on this request" (missing
// cookie or an absent/expired/invalid token). It is never surfaced to clients
// beyond a uniform 401.
var errNoSession = errors.New("no session")

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// newSessionToken mints a random opaque bearer token. The raw base64url string
// goes in the cookie; only its SHA-256 is persisted, so a DB leak yields no
// usable cookie. The token is unsigned and opaque — there is no signing key.
func newSessionToken() (raw string, hash []byte, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashSessionToken(raw), nil
}

func hashSessionToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func (s *Server) setSessionCookie(w http.ResponseWriter, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.sessionTTL.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// sessionFromRequest resolves the request's session cookie to its user/tenant.
// This is the factored resolution AUTH-3 wraps into auth middleware. A missing
// cookie or an absent/expired session both return errNoSession.
func (s *Server) sessionFromRequest(r *http.Request) (store.SessionUser, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return store.SessionUser{}, errNoSession
	}

	su, err := s.auth.GetSession(r.Context(), hashSessionToken(cookie.Value))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.SessionUser{}, errNoSession
		}
		return store.SessionUser{}, err
	}
	return su, nil
}

// handleLogin authenticates email+password and, on success, mints a fresh
// session and sets the cookie. All three failure shapes — unknown email, user
// with no password_hash, and wrong password — return a byte-identical 401 and
// each burns one argon2id verify, so neither the response nor gross timing
// reveals whether an account exists.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "bad request", "request body must be valid JSON")
		return
	}

	email := strings.TrimSpace(req.Email)
	if email == "" || req.Password == "" {
		writeProblem(w, http.StatusBadRequest, "bad request", "email and password are required")
		return
	}

	creds, err := s.auth.GetUserCredentials(r.Context(), email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Unknown email: burn a verify against the dummy hash so timing matches
		// the real-user path, then fail uniformly.
		_, _ = auth.VerifyPassword(auth.DummyHash, req.Password)
		s.writeLoginFailed(w)
		return
	case err != nil:
		s.writeInternalError(w, "login: get user credentials", err)
		return
	}

	hash := auth.DummyHash
	if creds.PasswordHash != nil {
		hash = *creds.PasswordHash
	}
	ok, err := auth.VerifyPassword(hash, req.Password)
	if err != nil || !ok || creds.PasswordHash == nil {
		// Malformed stored hash, mismatch, or a user with no credential yet all
		// fail identically. The verify above already ran for timing uniformity.
		s.writeLoginFailed(w)
		return
	}

	raw, tokenHash, err := newSessionToken()
	if err != nil {
		s.writeInternalError(w, "login: mint session token", err)
		return
	}
	if err := s.auth.CreateSession(r.Context(), store.CreateSessionParams{
		TokenHash: tokenHash,
		UserID:    creds.UserID,
		ExpiresAt: nowUTC().Add(s.sessionTTL),
	}); err != nil {
		s.writeInternalError(w, "login: create session", err)
		return
	}

	s.setSessionCookie(w, raw)
	writeJSON(w, http.StatusOK, userTenantResponse{
		User:   userResponse{ID: creds.UserID.String(), Email: creds.Email},
		Tenant: tenantResponse{ID: creds.TenantID.String(), Name: creds.TenantName},
	})
}

// handleLogout runs after requireSession, deletes the current session row, and
// clears the cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if err := s.auth.DeleteSession(r.Context(), hashSessionToken(cookie.Value)); err != nil {
			s.writeInternalError(w, "logout: delete session", err)
			return
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the current session's user, tenant, and the tenant's
// businesses, or 401 if there is no live session. It does not extend expiry.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if s.businesses == nil {
		s.writeInternalError(w, "me: store missing", errors.New("business store is required"))
		return
	}
	su, ok := sessionUserFromContext(r.Context())
	if !ok {
		s.writeInternalError(w, "me: missing session context", errors.New("missing session context"))
		return
	}

	businesses, err := s.businesses.ListBusinesses(r.Context(), su.TenantID)
	if err != nil {
		s.writeInternalError(w, "me: list businesses", err)
		return
	}

	resp := meResponse{
		userTenantResponse: userTenantResponse{
			User:   userResponse{ID: su.UserID.String(), Email: su.Email},
			Tenant: tenantResponse{ID: su.TenantID.String(), Name: su.TenantName},
		},
		Businesses: make([]businessResponse, 0, len(businesses)),
	}
	for _, b := range businesses {
		resp.Businesses = append(resp.Businesses, businessResponse{
			ID:     b.ID.String(),
			Name:   b.Name,
			Status: string(b.Status),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeLoginFailed writes the single uniform 401. Every auth-failure caller uses
// it so the bytes are identical across unknown-email, no-credential, and
// wrong-password cases — no user-existence oracle.
func (s *Server) writeLoginFailed(w http.ResponseWriter) {
	writeProblem(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
}

func (s *Server) writeInternalError(w http.ResponseWriter, msg string, err error) {
	// Log the real error but never the password or token.
	slog.Error("api: "+msg, "error", err)
	writeProblem(w, http.StatusInternalServerError, "internal server error", "an unexpected error occurred")
}
