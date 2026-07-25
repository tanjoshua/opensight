// Package api hosts the Connect RPC handlers and tenant scoping for the
// OpenSight API server (01-D2). Routes() mounts /healthz, the /rpc tree
// (rpc.go), and the embedded SPA fallback (static.go).
package api

import (
	"context"
	"encoding/json"
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

// Routes is the single place routes are registered: /healthz, the /rpc tree,
// and the SPA fallback for everything else.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	spa := newSPAHandler()
	r.NotFound(spa.ServeHTTP)
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, http.StatusMethodNotAllowed, "method not allowed", "method not allowed")
	})

	r.Get("/healthz", handleHealthz)

	r.Mount("/rpc", s.rpcHandler())

	return r
}

// nowUTC is the single clock source for session expiry, so it is trivial to
// stub in a future test.
func nowUTC() time.Time { return time.Now().UTC() }

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// writeProblem writes an RFC 7807 problem+json response (design 06). It
// survives REST's removal (RPC-8) because the SPA static-file route
// (static.go) and the router-level MethodNotAllowed handler above still use
// it for the handful of non-RPC responses that aren't the SPA shell itself.
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
