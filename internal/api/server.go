// Package api hosts the Connect RPC handlers and tenant scoping for the
// OpenSight API server (01-D2). Routes() mounts /healthz, the /rpc tree
// (rpc.go), and the embedded SPA fallback (static.go).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"opensight/internal/billing"
	"opensight/internal/billing/reconcile"
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

// accountStore is the consumer-side seam over *store.AccountStore's signup
// method. It is a separate seam from authStore rather than a fifth method
// bolted on there: AuthStore's doc comment (internal/store/auth.go) pins that
// type to exactly its four session/credential methods, and CreateAccount is a
// tenant-creation write, not a credential read.
type accountStore interface {
	CreateAccount(ctx context.Context, params store.CreateAccountParams) (store.Tenant, store.User, error)
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

// subscriptionStore is the seam over *store.SubscriptionStore. Server.tenantPlan
// resolves the returned plan_code against the billing catalog, so onboarding
// sizes the generated prompt count from billing.Plan.PromptLimit (never
// hardcoded — design 03). SetStripeCustomerID (BILL-4) is StartCheckout's
// write-once Customer id persistence.
type subscriptionStore interface {
	GetByTenant(ctx context.Context, tenantID domain.ID) (store.Subscription, error)
	SetStripeCustomerID(ctx context.Context, tenantID domain.ID, customerID string) (string, error)
}

// billingProvider is the slice of billing.Provider the RPC layer needs.
// GetSubscriptionForCustomer belongs to reconcile, not here; CreatePortalSession
// arrives with BILL-8.
type billingProvider interface {
	CreateCustomer(ctx context.Context, params billing.CreateCustomerParams) (billing.Customer, error)
	CreateCheckoutSession(ctx context.Context, params billing.CreateCheckoutSessionParams) (billing.CheckoutSession, error)
	GetCheckoutSession(ctx context.Context, sessionID string) (billing.CheckoutSession, error)
}

// billingReconciler is the seam over *reconcile.Reconciler: ConfirmCheckout's
// one write path (design 08 "Reconcile").
type billingReconciler interface {
	Tenant(ctx context.Context, tenantID domain.ID) (store.Subscription, error)
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
	accounts    accountStore
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
	// subscriptions, proposals, temporal, and temporalTaskQueue drive onboarding
	// (ONB-4): create a draft business and start GenerateProfileWorkflow, poll
	// its status, and regenerate. temporal is nil in handler unit tests that
	// don't exercise these endpoints.
	subscriptions     subscriptionStore
	proposals         proposalStore
	apply             applyStore
	temporal          temporalClient
	temporalTaskQueue string
	// secureCookies gates the Secure cookie attribute. It is false only in dev
	// (FND-2 local dev is plain HTTP); prod runs behind Caddy TLS.
	secureCookies bool
	// sessionTTL is a field (not a bare const) so tests can shrink it.
	sessionTTL time.Duration
	// billing, reconciler, stripePriceIDs, and appBaseURL drive checkout
	// (BILL-4): create/reuse the tenant's Stripe Customer, start a Checkout
	// Session against the plan's Price, and reconcile on return.
	billing        billingProvider
	reconciler     billingReconciler
	stripePriceIDs map[string]string
	appBaseURL     string
}

// Deps are api.New's dependencies. A struct rather than a positional argument
// list: the billing stories add several more dependencies, and two adjacent
// same-typed strings (temporalTaskQueue, appBaseURL, and BILL-5's
// stripeWebhookSecret) can silently swap at a positional call site.
type Deps struct {
	Auth              *store.AuthStore
	Accounts          *store.AccountStore
	Businesses        *store.BusinessStore
	Subscriptions     *store.SubscriptionStore
	Proposals         *store.ProfileProposalStore
	Apply             *store.ApplyProposalStore
	Prompts           *store.PromptStore
	Competitors       *store.CompetitorStore
	Runs              *store.RunStore
	Results           *store.ResultStore
	Metrics           *metrics.Metrics
	Temporal          client.Client
	TemporalTaskQueue string
	SecureCookies     bool

	// Billing (BILL-4).
	Billing        billing.Provider
	Reconciler     *reconcile.Reconciler
	StripePriceIDs map[string]string
	AppBaseURL     string
}

// New builds a Server from d. SecureCookies should be true everywhere except
// plain-HTTP local dev (computed in serve() as cfg.Env != "dev").
func New(d Deps) *Server {
	return &Server{
		auth:              d.Auth,
		accounts:          d.Accounts,
		businesses:        d.Businesses,
		subscriptions:     d.Subscriptions,
		proposals:         d.Proposals,
		apply:             d.Apply,
		prompts:           d.Prompts,
		competitors:       d.Competitors,
		runs:              d.Runs,
		results:           d.Results,
		metrics:           d.Metrics,
		promptMetrics:     d.Metrics,
		competitorMetrics: d.Metrics,
		citationMetrics:   d.Metrics,
		runMetrics:        d.Metrics,
		temporal:          d.Temporal,
		temporalTaskQueue: d.TemporalTaskQueue,
		secureCookies:     d.SecureCookies,
		sessionTTL:        defaultSessionTTL,
		billing:           d.Billing,
		reconciler:        d.Reconciler,
		stripePriceIDs:    d.StripePriceIDs,
		appBaseURL:        d.AppBaseURL,
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

// tenantPlan loads the tenant's subscription and resolves its plan_code
// against the billing catalog. Call sites that used to read s.plans.GetTenantPlan
// call this one-liner instead (design 08 "Entitlements move from a table to
// code").
func (s *Server) tenantPlan(ctx context.Context, tenantID domain.ID) (billing.Plan, error) {
	sub, err := s.subscriptions.GetByTenant(ctx, tenantID)
	if err != nil {
		return billing.Plan{}, err
	}
	plan, err := billing.PlanFor(sub.PlanCode)
	if err != nil {
		return billing.Plan{}, fmt.Errorf("resolve plan: %w", err)
	}
	return plan, nil
}

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
