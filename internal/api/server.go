// Package api hosts the Connect RPC handlers and account scoping for the
// OpenSight API server (01-D2). Routes() mounts /healthz, the /rpc tree
// (rpc.go), and the embedded SPA fallback (static.go).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"opensight/internal/billing"
	"opensight/internal/billing/reconcile"
	"opensight/internal/llm"
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

// billingProvider is the Stripe operations the RPC layer consumes. It stays a
// seam — unlike the store and metrics, which the Server holds concretely —
// because the calls behind it leave the process, so tests need a stub.
// GetSubscriptionForCustomer belongs to reconcile's separate narrow seam.
type billingProvider interface {
	CreateCustomer(ctx context.Context, params billing.CreateCustomerParams) (billing.Customer, error)
	CreateCheckoutSession(ctx context.Context, params billing.CreateCheckoutSessionParams) (billing.CheckoutSession, error)
	GetCheckoutSession(ctx context.Context, sessionID string) (billing.CheckoutSession, error)
	CreatePortalSession(ctx context.Context, params billing.CreatePortalSessionParams) (billing.PortalSession, error)
	// GetPrice backs GetBilling's price display; cachedPrice
	// (billing_rpc.go) is the only caller.
	GetPrice(ctx context.Context, priceID string) (billing.Price, error)
}

// temporalClient is the narrow slice of client.Client the API server needs:
// start GenerateProfileWorkflow/RunWorkflow, describe generation status, query
// the current generation stage, and (via ScheduleClient) create the monitoring
// Schedule on apply. Like billingProvider it stays a seam: dispatching a
// workflow is an out-of-process side effect tests must observe without a
// Temporal server.
type temporalClient interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error)
	TerminateWorkflow(ctx context.Context, workflowID, runID, reason string, details ...interface{}) error
	DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
	QueryWorkflow(ctx context.Context, workflowID, runID, queryType string, args ...interface{}) (converter.EncodedValue, error)
	ScheduleClient() client.ScheduleClient
}

// Server holds the API dependencies and configuration.
type Server struct {
	// store and metrics are the two concrete backends every handler reads and
	// writes through. Both are pure Postgres, so handler tests exercise them
	// against a real test database rather than a fake.
	store   *store.Store
	metrics *metrics.Metrics
	// temporal and temporalTaskQueue drive onboarding: create a draft business
	// and start GenerateProfileWorkflow, poll its status, and regenerate.
	temporal          temporalClient
	temporalTaskQueue string
	// questions runs GenerateQuestions' on-demand customer-question call
	// (design 03): a synchronous RPC, not a Temporal activity, so the runner
	// lives on the API server rather than workflows.Activities.
	questions llm.QuestionsRunner
	// secureCookies gates the Secure cookie attribute. It is false only in dev
	// (local dev is plain HTTP); prod runs behind Caddy TLS.
	secureCookies bool
	// sessionTTL is a field (not a bare const) so tests can shrink it.
	sessionTTL time.Duration
	// billing, reconciler, stripePriceIDs, and appBaseURL drive checkout:
	// create/reuse the account's Stripe Customer, start a Checkout
	// Session against the plan's Price, and reconcile on return.
	billing        billingProvider
	reconciler     *reconcile.Reconciler
	stripePriceIDs map[string]string
	appBaseURL     string
	// stripePortalConfigurationID pins CreatePortalSession to the
	// repo-owned Billing Portal Configuration rather than the account
	// default (design 08 "Customer Portal").
	stripePortalConfigurationID string
	// webhooks verifies /webhooks/stripe deliveries.
	webhooks billing.WebhookVerifier
	// googleAuth drives /auth/google/start and /auth/google/callback (design
	// 07 "Auth and accounts") — the sole sign-in path.
	googleAuth googleAuthenticator
	// priceCache holds GetBilling's fetched Stripe Prices, keyed by
	// price id. Safe to keep for the process lifetime: Stripe Prices are
	// immutable, so a cache entry can never go stale.
	priceCache struct {
		mu     sync.Mutex
		prices map[string]billing.Price
	}
}

// Deps are api.New's dependencies. A struct rather than a positional argument
// list: there are many of them, and adjacent same-typed fields (temporalTaskQueue, appBaseURL) can silently swap at a
// positional call site.
type Deps struct {
	Store             *store.Store
	Metrics           *metrics.Metrics
	Temporal          client.Client
	TemporalTaskQueue string
	// Questions backs GenerateQuestions. See Server.questions.
	Questions     llm.QuestionsRunner
	SecureCookies bool

	// Billing.
	Billing        billingProvider
	Reconciler     *reconcile.Reconciler
	StripePriceIDs map[string]string
	AppBaseURL     string
	// StripePortalConfigurationID — see Server.stripePortalConfigurationID.
	StripePortalConfigurationID string

	// Webhook.
	Webhooks billing.WebhookVerifier

	// GoogleAuth backs /auth/google/*. See Server.googleAuth.
	GoogleAuth googleAuthenticator
}

// New builds a Server from d. SecureCookies should be true everywhere except
// plain-HTTP local dev (computed in serve() as cfg.Env != "dev").
func New(d Deps) *Server {
	s := &Server{
		store:                       d.Store,
		metrics:                     d.Metrics,
		temporal:                    d.Temporal,
		temporalTaskQueue:           d.TemporalTaskQueue,
		questions:                   d.Questions,
		secureCookies:               d.SecureCookies,
		sessionTTL:                  defaultSessionTTL,
		billing:                     d.Billing,
		reconciler:                  d.Reconciler,
		stripePriceIDs:              d.StripePriceIDs,
		appBaseURL:                  d.AppBaseURL,
		webhooks:                    d.Webhooks,
		stripePortalConfigurationID: d.StripePortalConfigurationID,
		googleAuth:                  d.GoogleAuth,
	}
	s.priceCache.prices = make(map[string]billing.Price)
	return s
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

	// Outside /rpc so the session interceptor never sees it: Stripe signs the
	// delivery, and that signature is the route's only authentication
	// (design 08 "Webhook").
	r.Post("/webhooks/stripe", s.handleStripeWebhook)

	// Google sign-in is plain HTTP browser navigation, not a Connect RPC
	// (google_auth.go) — outside /rpc for the same reason as the webhook.
	r.Get("/auth/google/start", s.handleGoogleStart)
	r.Get("/auth/google/callback", s.handleGoogleCallback)

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

// writeProblem writes an RFC 7807 problem+json response (design 06). Its
// callers are the handful of non-RPC error responses: the SPA static-file
// route (static.go) and the router-level MethodNotAllowed handler above.
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
