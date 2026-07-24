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

	"opensight/internal/domain"
	"opensight/internal/metrics"
	"opensight/internal/store"

	"github.com/go-chi/chi/v5"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
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

// businessStore is the consumer-side seam over *store.BusinessStore. /me lists
// the tenant's businesses so the SPA can bootstrap section URLs (design 06:
// URLs carry businessID; MVP has one business per tenant). GetBusiness is the
// tenant-ownership gate for business-scoped endpoints that have no other
// natural gate (it returns ErrNotFound for a missing/cross-tenant business).
type businessStore interface {
	ListBusinesses(ctx context.Context, tenantID domain.ID) ([]store.Business, error)
	GetBusiness(ctx context.Context, tenantID, businessID domain.ID) (store.Business, error)
	CreateBusiness(ctx context.Context, params store.CreateBusinessParams) (store.Business, error)
	UpdateActiveProfile(ctx context.Context, params store.UpdateBusinessProfileParams) (store.Business, error)
}

// planStore is the seam over *store.AdminStore's plan lookup. Onboarding sizes
// the generated prompt count from plan.prompt_limit (never hardcoded — design 03).
type planStore interface {
	GetTenantPlan(ctx context.Context, tenantID domain.ID) (store.Plan, error)
}

// proposalStore is the seam over *store.ProfileProposalStore for the proposal
// endpoints: read the pending proposal, and discard it on regenerate.
type proposalStore interface {
	GetPending(ctx context.Context, tenantID, businessID domain.ID) (store.ProfileProposal, error)
	DiscardPending(ctx context.Context, tenantID, businessID domain.ID) error
}

// temporalClient is the narrow slice of client.Client the API server needs:
// start GenerateProfileWorkflow/RunWorkflow, describe generation status, query
// the current generation stage, and (via ScheduleClient) create the monitoring
// Schedule on apply.
type temporalClient interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error)
	DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
	QueryWorkflow(ctx context.Context, workflowID, runID, queryType string, args ...interface{}) (converter.EncodedValue, error)
	ScheduleClient() client.ScheduleClient
}

// applyStore is the seam over *store.ApplyProposalStore: the transactional
// activate-business-and-insert-prompts operation ONB-6 runs on apply.
type applyStore interface {
	Apply(ctx context.Context, params store.ApplyProposalParams) (store.ApplyProposalResult, error)
}

// Server holds the API dependencies and configuration.
type Server struct {
	auth        authStore
	businesses  businessStore
	prompts     promptStore
	competitors competitorStore
	runs        runStore
	results     resultStore
	metrics     overviewMetrics
	// runMetrics is the metrics seam for the Runs endpoint's per-run visibility %
	// (MET-5) — a second view over the same *metrics.Metrics as Overview.
	runMetrics runsMetrics
	// promptMetrics is a second seam over the same *metrics.Metrics: the Prompts
	// section needs PromptLatestStats/PromptTrends, which the Overview seam does
	// not expose. Splitting the seams keeps each handler's fake minimal.
	promptMetrics promptsMetrics
	// competitorMetrics is a third seam over the same *metrics.Metrics for the
	// Competitors section (MET-4): just CompetitorStats, so its fake stays minimal.
	competitorMetrics competitorsMetrics
	// citationMetrics is a fourth seam for MET-6's citation-sources drill-down.
	citationMetrics citationsMetrics
	// plans, proposals, temporal, and temporalTaskQueue drive onboarding (ONB-4):
	// create a draft business and start GenerateProfileWorkflow, poll its status,
	// and regenerate. temporal is nil in handler unit tests that don't exercise
	// these endpoints.
	plans             planStore
	proposals         proposalStore
	apply             applyStore
	temporal          temporalClient
	temporalTaskQueue string
	// secureCookies gates the Secure cookie attribute. It is false only in dev
	// (FND-2 local dev is plain HTTP); prod runs behind Caddy TLS.
	secureCookies bool
	// sessionTTL is a field (not a bare const) so tests can shrink it.
	sessionTTL time.Duration
}

// New builds a Server. secureCookies should be true everywhere except
// plain-HTTP local dev (computed in serve() as cfg.Env != "dev").
func New(auth *store.AuthStore, businesses *store.BusinessStore, plans *store.AdminStore, proposals *store.ProfileProposalStore, apply *store.ApplyProposalStore, prompts *store.PromptStore, competitors *store.CompetitorStore, runs *store.RunStore, results *store.ResultStore, metrics *metrics.Metrics, temporal client.Client, temporalTaskQueue string, secureCookies bool) *Server {
	return &Server{
		auth:              auth,
		businesses:        businesses,
		plans:             plans,
		proposals:         proposals,
		apply:             apply,
		prompts:           prompts,
		competitors:       competitors,
		runs:              runs,
		results:           results,
		metrics:           metrics,
		promptMetrics:     metrics,
		competitorMetrics: metrics,
		citationMetrics:   metrics,
		runMetrics:        metrics,
		temporal:          temporal,
		temporalTaskQueue: temporalTaskQueue,
		secureCookies:     secureCookies,
		sessionTTL:        defaultSessionTTL,
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
			r.Post("/businesses", s.handleCreateBusiness)
			r.Get("/businesses/{businessID}", s.handleGetBusiness)
			r.Patch("/businesses/{businessID}", s.handlePatchBusiness)
			r.Get("/businesses/{businessID}/proposal", s.handleGetProposal)
			r.Post("/businesses/{businessID}/proposal/regen", s.handleRegenProposal)
			r.Post("/businesses/{businessID}/apply", s.handleApplyBusiness)
			r.Get("/businesses/{businessID}/overview", s.handleGetOverview)
			r.Get("/businesses/{businessID}/prompts", s.handleListPrompts)
			r.Post("/businesses/{businessID}/prompts", s.handleAddPrompt)
			r.Get("/prompts/{promptID}", s.handleGetPrompt)
			r.Post("/prompts/{promptID}/replace", s.handleReplacePrompt)
			r.Get("/businesses/{businessID}/competitors", s.handleListCompetitors)
			r.Post("/businesses/{businessID}/competitors", s.handleAddCompetitor)
			r.Post("/competitors/{competitorID}/track", s.handleTrackCompetitor)
			r.Post("/competitors/{competitorID}/dismiss", s.handleDismissCompetitor)
			r.Post("/competitors/{competitorID}/suggested-aliases/approve", s.handleApproveSuggestedAlias)
			r.Post("/competitors/{competitorID}/suggested-aliases/reject", s.handleRejectSuggestedAlias)
			r.Patch("/competitors/{competitorID}", s.handlePatchCompetitorAliases)
			r.Get("/businesses/{businessID}/citations", s.handleListCitations)
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

// userTenantResponse is the shared core of /me and a successful login: the
// session's own user and tenant, nothing cross-tenant.
type userTenantResponse struct {
	User   userResponse   `json:"user"`
	Tenant tenantResponse `json:"tenant"`
}

// meResponse is /me's body: the login shape plus the tenant's businesses and
// prompt entitlement, so the SPA can resolve business-scoped URLs and validate
// onboarding's final prompt count without hardcoding a plan limit.
type meResponse struct {
	userTenantResponse
	Businesses  []businessResponse `json:"businesses"`
	PromptLimit int                `json:"prompt_limit"`
}

type businessResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
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
