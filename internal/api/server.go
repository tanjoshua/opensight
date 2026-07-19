// Package api hosts the HTTP handlers, middleware, and tenant scoping for the
// OpenSight API server (01-D2). AUTH-1 introduces the chi router and the first
// real routes (login/logout/me); later stories mount their handlers and shared
// middleware inside Routes().
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"opensight/internal/store"

	"github.com/go-chi/chi/v5"
)

// defaultSessionTTL is the absolute session lifetime (design 07 auth plan:
// 30-day absolute expiry, no sliding renewal — this is a weekly-cadence
// product, so re-login roughly monthly is fine).
const defaultSessionTTL = 30 * 24 * time.Hour

// authStore is the consumer-side seam over *store.AuthStore, so handler unit
// tests run against a fake with no Postgres. It mirrors the four AuthStore
// methods exactly.
type authStore interface {
	GetUserCredentials(ctx context.Context, email string) (store.UserCredentials, error)
	CreateSession(ctx context.Context, params store.CreateSessionParams) error
	GetSession(ctx context.Context, tokenHash []byte) (store.SessionUser, error)
	DeleteSession(ctx context.Context, tokenHash []byte) error
}

// Server holds the API dependencies and configuration.
type Server struct {
	auth    authStore
	runs    runStore
	results resultStore
	// secureCookies gates the Secure cookie attribute. It is false only in dev
	// (FND-2 local dev is plain HTTP); prod runs behind Caddy TLS.
	secureCookies bool
	// sessionTTL is a field (not a bare const) so tests can shrink it.
	sessionTTL time.Duration
}

// New builds a Server. secureCookies should be true everywhere except
// plain-HTTP local dev (computed in serve() as cfg.Env != "dev").
func New(auth *store.AuthStore, runs *store.RunStore, results *store.ResultStore, secureCookies bool) *Server {
	return &Server{
		auth:          auth,
		runs:          runs,
		results:       results,
		secureCookies: secureCookies,
		sessionTTL:    defaultSessionTTL,
	}
}

// Routes is the single place routes are registered. Later stories add
// r.Use(...) middleware and further routes here.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	spa := newSPAHandler()
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if isAPIRoute(r.URL.Path) {
			writeProblem(w, http.StatusNotFound, "not found", "not found")
			return
		}
		spa.ServeHTTP(w, r)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, http.StatusMethodNotAllowed, "method not allowed", "method not allowed")
	})

	r.Get("/healthz", handleHealthz)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(requireRequestedWith)
		r.Post("/login", s.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.requireSession)
			r.Get("/me", s.handleMe)
			r.Get("/businesses/{businessID}/runs", s.handleListRuns)
			r.Get("/businesses/{businessID}/results", s.handleListResults)
			r.Get("/results/{resultID}", s.handleGetResult)
			r.Post("/logout", s.handleLogout)
		})
	})

	return r
}

// nowUTC is the single clock source for session expiry, so it is trivial to
// stub in a future test.
func nowUTC() time.Time { return time.Now().UTC() }

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// userTenantResponse is the shared body of /me and a successful login: the
// session's own user and tenant, nothing cross-tenant.
type userTenantResponse struct {
	User   userResponse   `json:"user"`
	Tenant tenantResponse `json:"tenant"`
}

type userResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type tenantResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// writeProblem writes an RFC 7807 problem+json response (design 06). This is the
// smallest useful shape; AUTH-3 promotes it to an app-wide helper.
func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("api: encode response", "error", err)
	}
}
