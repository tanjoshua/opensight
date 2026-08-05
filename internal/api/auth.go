package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"

	"opensight/internal/billing"
	"opensight/internal/store"
)

const sessionCookieName = "opensight_session"

// errNoSession is the sentinel for "no live session on this request" (missing
// cookie or an absent/expired/invalid token). It is never surfaced to clients
// beyond a uniform 401.
var errNoSession = errors.New("no session")

// requestContext holds the session and its derived access under one context
// key, so the two can never be set independently of each other by
// accident. hasAccess distinguishes "access was derived this request" from
// the zero billing.AccessNever, for callers (like handler tests) that set
// only a session via withSessionUser and never call accessFromContext.
type requestContext struct {
	su        store.SessionUser
	access    billing.Access
	hasAccess bool
}

type requestContextKey struct{}

// withSessionUser stores su, preserving whatever access was already set on
// ctx (if any) — accessInterceptor calls this before withAccess. Handler
// tests that bypass the interceptor and call this alone get a context with
// no access, which is fine: they don't call accessFromContext.
func withSessionUser(ctx context.Context, su store.SessionUser) context.Context {
	rc, _ := ctx.Value(requestContextKey{}).(requestContext)
	rc.su = su
	return context.WithValue(ctx, requestContextKey{}, rc)
}

// withAccess stores the request's derived access, preserving whatever
// session was already set on ctx.
func withAccess(ctx context.Context, access billing.Access) context.Context {
	rc, _ := ctx.Value(requestContextKey{}).(requestContext)
	rc.access = access
	rc.hasAccess = true
	return context.WithValue(ctx, requestContextKey{}, rc)
}

func sessionUserFromContext(ctx context.Context) (store.SessionUser, bool) {
	rc, ok := ctx.Value(requestContextKey{}).(requestContext)
	if !ok {
		return store.SessionUser{}, false
	}
	return rc.su, true
}

// accessFromContext returns the request's derived access, set by
// accessInterceptor for every non-public procedure. Handler unit tests that
// construct a context via withSessionUser alone (bypassing the interceptor)
// get ok=false.
func accessFromContext(ctx context.Context) (billing.Access, bool) {
	rc, ok := ctx.Value(requestContextKey{}).(requestContext)
	if !ok || !rc.hasAccess {
		return billing.AccessNever, false
	}
	return rc.access, true
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

// sessionCookie builds the login cookie.
func (s *Server) sessionCookie(raw string) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.sessionTTL.Seconds()),
	}
}

// expiredSessionCookie builds the MaxAge=-1 clearing cookie.
func (s *Server) expiredSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// sessionTokenFromHeader extracts the raw session token from a request's
// Cookie header. Returns "" when absent or empty.
func sessionTokenFromHeader(h http.Header) string {
	cookie, err := (&http.Request{Header: h}).Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// sessionFromHeader resolves a request's session cookie to its user/account.
// Missing cookie or absent/expired session both return errNoSession. This is
// the RPC session interceptor's resolution path (rpc.go).
func (s *Server) sessionFromHeader(ctx context.Context, h http.Header) (store.SessionUser, error) {
	raw := sessionTokenFromHeader(h)
	if raw == "" {
		return store.SessionUser{}, errNoSession
	}

	su, err := s.store.GetSession(ctx, hashSessionToken(raw))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.SessionUser{}, errNoSession
		}
		return store.SessionUser{}, err
	}
	return su, nil
}
