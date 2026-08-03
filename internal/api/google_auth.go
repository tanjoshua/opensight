// Google sign-in (design 07 "Auth and accounts"). This is a plain HTTP
// redirect flow — GET /auth/google/start and GET /auth/google/callback,
// mounted outside /rpc in Routes() exactly like /webhooks/stripe — not a
// Connect RPC, because Google's authorization-code flow is browser
// navigation, not a same-origin API call. On success the callback mints the
// same opaque opensight_session cookie Login used to.
package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"opensight/internal/domain"
	"opensight/internal/store"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// oauthStateCookieName holds the CSRF state and PKCE verifier for an
// in-flight Google sign-in. Scoped to /auth/google and cleared by the
// callback unconditionally, so it never outlives one attempt.
const oauthStateCookieName = "opensight_oauth"

// oauthStateTTL bounds how long a user has to complete the Google consent
// screen before the flow must be restarted from /auth/google/start.
const oauthStateTTL = 10 * time.Minute

// googleIdentity is the sign-in-relevant subset of an ID token's claims.
type googleIdentity struct {
	Sub           string
	Email         string
	EmailVerified bool
}

// googleAuthenticator is the Google OAuth 2.0 / OIDC operations the callback
// route consumes. A seam like billingProvider (server.go): the exchange
// leaves the process, so the integration test stubs it instead of hitting
// Google.
type googleAuthenticator interface {
	// AuthCodeURL builds the accounts.google.com consent URL for a code+PKCE
	// authorization-code flow keyed to state and codeVerifier.
	AuthCodeURL(state, codeVerifier string) string
	// Exchange redeems an authorization code for the signed-in user's
	// identity. codeVerifier must be the same value passed to AuthCodeURL for
	// this attempt, so Google can check it against the challenge sent there.
	Exchange(ctx context.Context, code, codeVerifier string) (googleIdentity, error)
}

// GoogleOAuth is the production googleAuthenticator, backed by
// golang.org/x/oauth2's Google endpoint.
type GoogleOAuth struct {
	cfg *oauth2.Config
}

var _ googleAuthenticator = (*GoogleOAuth)(nil)

// NewGoogleOAuth builds the production Deps.GoogleAuth implementation.
// redirectURL must exactly match an authorized redirect URI configured on
// the Google Cloud OAuth client.
func NewGoogleOAuth(clientID, clientSecret, redirectURL string) *GoogleOAuth {
	return &GoogleOAuth{cfg: &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{"openid", "email"},
		Endpoint:     google.Endpoint,
	}}
}

func (g *GoogleOAuth) AuthCodeURL(state, codeVerifier string) string {
	return g.cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(codeVerifier))
}

func (g *GoogleOAuth) Exchange(ctx context.Context, code, codeVerifier string) (googleIdentity, error) {
	tok, err := g.cfg.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return googleIdentity{}, fmt.Errorf("exchange code: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return googleIdentity{}, errors.New("token response had no id_token")
	}
	return parseGoogleIDToken(raw, g.cfg.ClientID)
}

// parseGoogleIDToken decodes an ID token's claims without checking its
// signature. That is safe here specifically because this token came back
// over the direct, TLS-authenticated, client-secret-authenticated channel to
// Google's token endpoint — not a value that passed through the browser or
// an attacker-controlled redirect. OpenID Connect Core 1.0 §3.1.3.7 permits
// skipping signature validation for exactly this case, since the channel's
// TLS server authentication already establishes the issuer. aud, iss, and
// exp are still checked explicitly below.
func parseGoogleIDToken(raw, clientID string) (googleIdentity, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return googleIdentity{}, errors.New("malformed id_token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return googleIdentity{}, fmt.Errorf("decode id_token payload: %w", err)
	}

	var claims struct {
		Iss           string `json:"iss"`
		Aud           string `json:"aud"`
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Exp           int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return googleIdentity{}, fmt.Errorf("decode id_token claims: %w", err)
	}

	if claims.Iss != "https://accounts.google.com" && claims.Iss != "accounts.google.com" {
		return googleIdentity{}, fmt.Errorf("unexpected id_token issuer %q", claims.Iss)
	}
	if claims.Aud != clientID {
		return googleIdentity{}, errors.New("id_token audience does not match this client")
	}
	if claims.Exp != 0 && time.Now().After(time.Unix(claims.Exp, 0)) {
		return googleIdentity{}, errors.New("id_token has expired")
	}
	if claims.Sub == "" || claims.Email == "" {
		return googleIdentity{}, errors.New("id_token missing sub or email")
	}

	return googleIdentity{Sub: claims.Sub, Email: strings.ToLower(claims.Email), EmailVerified: claims.EmailVerified}, nil
}

// handleGoogleStart begins the flow: a state token and PKCE verifier are
// minted, stashed together in a short-lived cookie, and the browser is sent
// to Google's consent screen. The verifier travels only in this cookie —
// never in the URL — so the callback is the only party that can complete the
// exchange it started.
func (s *Server) handleGoogleStart(w http.ResponseWriter, r *http.Request) {
	state, err := randomURLSafeToken(24)
	if err != nil {
		s.googleAuthFailed(w, r, "google: mint oauth state", err)
		return
	}
	verifier := oauth2.GenerateVerifier()

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state + "." + verifier,
		Path:     "/auth/google",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oauthStateTTL.Seconds()),
	})
	http.Redirect(w, r, s.googleAuth.AuthCodeURL(state, verifier), http.StatusFound)
}

// handleGoogleCallback completes the flow: verify state, exchange the code,
// resolve or provision the account, and mint a session exactly as Login used
// to. Any failure redirects to the login page's error state rather than
// surfacing a raw error page — this is a browser navigation, not an RPC.
func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// The flow ends at this request either way; clear the state cookie up
	// front so a failed attempt can't be replayed.
	http.SetCookie(w, s.expiredOAuthStateCookie())

	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		s.googleAuthFailed(w, r, "google: missing oauth state cookie", err)
		return
	}
	wantState, verifier, ok := strings.Cut(cookie.Value, ".")
	if !ok || wantState == "" || verifier == "" {
		s.googleAuthFailed(w, r, "google: malformed oauth state cookie", errors.New("malformed cookie"))
		return
	}
	if r.URL.Query().Get("state") != wantState {
		s.googleAuthFailed(w, r, "google: oauth state mismatch", errors.New("state mismatch"))
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		s.googleAuthFailed(w, r, "google: consent denied", errors.New(errParam))
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.googleAuthFailed(w, r, "google: missing code", errors.New("missing code"))
		return
	}

	identity, err := s.googleAuth.Exchange(ctx, code, verifier)
	if err != nil {
		s.googleAuthFailed(w, r, "google: exchange code", err)
		return
	}
	if !identity.EmailVerified {
		s.googleAuthFailed(w, r, "google: email not verified", errors.New("email not verified"))
		return
	}

	userID, err := s.resolveOrCreateGoogleUser(ctx, identity)
	if err != nil {
		s.googleAuthFailed(w, r, "google: resolve user", err)
		return
	}

	raw, tokenHash, err := newSessionToken()
	if err != nil {
		s.googleAuthFailed(w, r, "google: mint session token", err)
		return
	}
	if err := s.store.CreateSession(ctx, store.CreateSessionParams{
		TokenHash: tokenHash,
		UserID:    userID,
		ExpiresAt: nowUTC().Add(s.sessionTTL),
	}); err != nil {
		s.googleAuthFailed(w, r, "google: create session", err)
		return
	}

	http.SetCookie(w, s.sessionCookie(raw))
	http.Redirect(w, r, s.appBaseURL+"/overview", http.StatusFound)
}

// resolveOrCreateGoogleUser implements the identity ladder from design 07:
// match by google_sub (every sign-in after the first), fall back to matching
// by email and linking the sub (an operator-created row, or a row from
// before this account ever signed in with Google), and otherwise provision a
// brand-new tenant + user (self-serve signup) exactly as Signup used to.
func (s *Server) resolveOrCreateGoogleUser(ctx context.Context, identity googleIdentity) (domain.ID, error) {
	if u, err := s.store.GetUserByGoogleSub(ctx, identity.Sub); err == nil {
		return u.UserID, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return domain.ID{}, err
	}

	if u, err := s.store.GetUserByEmail(ctx, identity.Email); err == nil {
		return u.UserID, s.linkGoogleSubIfUnset(ctx, u, identity.Sub)
	} else if !errors.Is(err, store.ErrNotFound) {
		return domain.ID{}, err
	}

	tenantID, err := domain.NewID()
	if err != nil {
		return domain.ID{}, err
	}
	userID, err := domain.NewID()
	if err != nil {
		return domain.ID{}, err
	}
	_, user, err := s.store.CreateAccount(ctx, store.CreateAccountParams{
		TenantID:  tenantID,
		UserID:    userID,
		Email:     identity.Email,
		GoogleSub: identity.Sub,
	})
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			// Lost a race with a concurrent first sign-in for the same email
			// (e.g. two tabs): the other request's insert won, so look it up
			// and link it exactly as the email-match branch above would have.
			u, getErr := s.store.GetUserByEmail(ctx, identity.Email)
			if getErr != nil {
				return domain.ID{}, getErr
			}
			return u.UserID, s.linkGoogleSubIfUnset(ctx, u, identity.Sub)
		}
		return domain.ID{}, err
	}
	return user.ID, nil
}

func (s *Server) linkGoogleSubIfUnset(ctx context.Context, u store.UserIdentity, sub string) error {
	if u.GoogleSub != nil {
		return nil
	}
	return s.store.SetUserGoogleSub(ctx, u.UserID, sub)
}

// googleAuthFailed logs the underlying cause and redirects the browser to the
// login page's error state.
func (s *Server) googleAuthFailed(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.Error("api: "+op, "error", err)
	http.Redirect(w, r, s.appBaseURL+"/login?error=google", http.StatusFound)
}

func (s *Server) expiredOAuthStateCookie() *http.Cookie {
	return &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/auth/google",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

func randomURLSafeToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
