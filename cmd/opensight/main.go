// Command opensight is the single OpenSight binary. Its mode is selected by
// subcommand:
//
//	opensight serve           # HTTP API server (01-D2)
//	opensight work            # Temporal worker (01-D2)
//	opensight migrate         # apply database migrations, then exit
//	opensight tenant create   # create an invite-only tenant
//	opensight user create     # create an invite-only user
//	opensight business create # seed an active business from a spec file
//	opensight seed dev        # seed a dev tenant and login account
//	opensight stripe portal-config  # apply the Billing Portal configuration
//	opensight stripe webhook-config # create/update the production webhook endpoint
//
// The same image runs serve and work via a command override in deployment
// (design 07 "Deployment").
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"opensight/internal/api"
	"opensight/internal/billing"
	"opensight/internal/billing/reconcile"
	"opensight/internal/billing/stripe"
	"opensight/internal/config"
	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/metrics"
	"opensight/internal/store"
	"opensight/internal/workflows"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

const (
	usage               = "usage: opensight <serve|work|migrate|tenant|user|business|seed|stripe>"
	tenantCreateUsage   = "usage: opensight tenant create --name <tenant-name>"
	userCreateUsage     = "usage: opensight user create --tenant <tenant-id> --email <email>"
	businessCreateUsage = "usage: opensight business create --tenant <tenant-id> --file <spec.yaml>"
	seedUsage           = seedDevArgsUsage
)

func main() {
	slog.SetDefault(newLogger(os.Stdout))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

// newLogger builds the default structured JSON logger writing to w. Structured
// slog JSON to stdout is the whole logging story for MVP (07 "Observability").
func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

type tenantCreateOptions struct {
	Name string
}

type userCreateOptions struct {
	TenantID domain.ID
	Email    string
}

// run dispatches the chosen subcommand. It is separated from main so it can be
// tested without spawning the process.
func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no subcommand given; %s", usage)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch cmd := args[0]; cmd {
	case "serve":
		return serve(ctx, cfg)
	case "work":
		return work(ctx, cfg)
	case "migrate":
		return migrate(ctx, cfg)
	case "tenant":
		return runTenantCommand(ctx, cfg, args[1:])
	case "user":
		return runUserCommand(ctx, cfg, args[1:])
	case "business":
		return runBusinessCommand(ctx, cfg, args[1:])
	case "seed":
		return runSeedCommand(ctx, cfg, args[1:])
	case "stripe":
		return runStripeCommand(ctx, cfg, args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q; %s", cmd, usage)
	}
}

func runTenantCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("tenant subcommand required; %s", tenantCreateUsage)
	}
	switch args[0] {
	case "create":
		opts, err := parseTenantCreateArgs(args[1:])
		if err != nil {
			return err
		}
		return createTenantCLI(ctx, cfg, opts, os.Stdout)
	default:
		return fmt.Errorf("unknown tenant subcommand %q; %s", args[0], tenantCreateUsage)
	}
}

func runUserCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("user subcommand required; %s", userCreateUsage)
	}
	switch args[0] {
	case "create":
		opts, err := parseUserCreateArgs(args[1:])
		if err != nil {
			return err
		}
		return createUserCLI(ctx, cfg, opts, os.Stdout)
	default:
		return fmt.Errorf("unknown user subcommand %q; %s", args[0], userCreateUsage)
	}
}

func runBusinessCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("business subcommand required; %s", businessCreateUsage)
	}
	switch args[0] {
	case "create":
		opts, err := parseBusinessCreateArgs(args[1:])
		if err != nil {
			return err
		}
		return createBusinessCLI(ctx, cfg, opts, os.Stdout)
	default:
		return fmt.Errorf("unknown business subcommand %q; %s", args[0], businessCreateUsage)
	}
}

func runSeedCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("seed subcommand required; %s", seedUsage)
	}
	switch args[0] {
	case "dev":
		email, err := parseSeedDevArgs(args[1:], os.Getenv)
		if err != nil {
			return err
		}
		return seedDevCLI(ctx, cfg, email, os.Stdout)
	default:
		return fmt.Errorf("unknown seed subcommand %q; %s", args[0], seedUsage)
	}
}

func parseTenantCreateArgs(args []string) (tenantCreateOptions, error) {
	flags := flag.NewFlagSet("tenant create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("name", "", "tenant name")
	if err := flags.Parse(args); err != nil {
		return tenantCreateOptions{}, fmt.Errorf("%s", tenantCreateUsage)
	}
	if flags.NArg() != 0 {
		return tenantCreateOptions{}, fmt.Errorf("unexpected argument %q; %s", flags.Arg(0), tenantCreateUsage)
	}

	opts := tenantCreateOptions{Name: strings.TrimSpace(*name)}
	if opts.Name == "" {
		return tenantCreateOptions{}, fmt.Errorf("--name is required; %s", tenantCreateUsage)
	}
	return opts, nil
}

func parseUserCreateArgs(args []string) (userCreateOptions, error) {
	flags := flag.NewFlagSet("user create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tenantRaw := flags.String("tenant", "", "tenant id")
	email := flags.String("email", "", "user email")
	if err := flags.Parse(args); err != nil {
		return userCreateOptions{}, fmt.Errorf("%s", userCreateUsage)
	}
	if flags.NArg() != 0 {
		return userCreateOptions{}, fmt.Errorf("unexpected argument %q; %s", flags.Arg(0), userCreateUsage)
	}
	if strings.TrimSpace(*tenantRaw) == "" {
		return userCreateOptions{}, fmt.Errorf("--tenant is required; %s", userCreateUsage)
	}
	if strings.TrimSpace(*email) == "" {
		return userCreateOptions{}, fmt.Errorf("--email is required; %s", userCreateUsage)
	}

	tenantID, err := uuid.Parse(strings.TrimSpace(*tenantRaw))
	if err != nil {
		return userCreateOptions{}, fmt.Errorf("--tenant must be a UUID: %w", err)
	}

	return userCreateOptions{TenantID: tenantID, Email: strings.TrimSpace(*email)}, nil
}

func createTenantCLI(ctx context.Context, cfg config.Config, opts tenantCreateOptions, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}

	account, closeStore, err := openAccountStore(cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	tenant, err := account.CreateTenant(ctx, store.CreateTenantParams{Name: opts.Name})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "tenant_id=%s\nname=%s\nplan=%s\n", tenant.ID, tenant.Name, billing.Starter.Code)
	return err
}

// createUserCLI creates a user row with no Google identity yet — the first
// sign-in with that email links it (design 07 "Auth and accounts").
func createUserCLI(ctx context.Context, cfg config.Config, opts userCreateOptions, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}

	account, closeStore, err := openAccountStore(cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	user, err := account.CreateUser(ctx, store.CreateUserParams{
		TenantID: opts.TenantID,
		Email:    opts.Email,
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "user_id=%s\ntenant_id=%s\nemail=%s\n", user.ID, user.TenantID, user.Email)
	return err
}

func openAccountStore(cfg config.Config) (*store.Store, func(), error) {
	db, err := store.Open(context.Background(), cfg.DatabaseURL, int32(cfg.DBMaxOpenConns))
	if err != nil {
		return nil, nil, err
	}
	return store.New(db), db.Close, nil
}

// serve runs the HTTP API server.
func serve(ctx context.Context, cfg config.Config) error {
	if ctx.Err() != nil {
		return nil
	}
	// Validate before opening the database or dialing Temporal so a bad
	// environment fails without touching any other service.
	if err := validateServeRuntimeConfig(cfg); err != nil {
		return err
	}

	db, err := store.Open(ctx, cfg.DatabaseURL, int32(cfg.DBMaxOpenConns))
	if err != nil {
		return err
	}
	defer db.Close()

	// serve starts GenerateProfileWorkflow on the same task queue the worker
	// consumes, so it needs a Temporal client too.
	temporalClient, err := dialTemporal(ctx, cfg)
	if err != nil {
		return err
	}
	defer temporalClient.Close()

	// Stripe is the sole runtime billing provider. Local development uses a
	// Stripe sandbox; the in-memory provider is retained only as a test fake.
	billingProvider, err := stripe.New(stripe.Config{APIKey: cfg.StripeSecretKey})
	if err != nil {
		return err
	}

	// GenerateQuestions is a synchronous RPC (design 03), not a Temporal
	// activity, so its runner is built here rather than in work()'s
	// workflows.Activities.
	questions, err := llm.NewQuestionsRunner(string(cfg.PromptRunnerMode), llm.OpenAIConfig{
		APIKey: cfg.OpenAIAPIKey,
		Model:  cfg.OpenAIQuestionsModel,
	})
	if err != nil {
		return fmt.Errorf("build questions runner: %w", err)
	}

	// One store over one pool, shared by the API server, the monitoring gate,
	// and the reconciler — no duplicate connection pooling.
	dataStore := store.New(db)
	monitoring := reconcile.NewMonitoring(dataStore, temporalClient)
	reconciler := reconcile.New(dataStore, billingProvider, monitoring, dataStore, nil)

	webhookVerifier := stripe.NewWebhookVerifier(cfg.StripeWebhookSecret)

	// The redirect URI is derived from AppBaseURL, not a separate env var —
	// it must exactly match an authorized redirect URI on the Google Cloud
	// OAuth client (design 07 "Auth and accounts").
	googleAuth := api.NewGoogleOAuth(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.AppBaseURL+"/auth/google/callback")

	// Secure cookies everywhere except plain-HTTP local dev. Prod runs behind
	// Caddy TLS, where Secure must be set.
	apiServer := api.New(api.Deps{
		Store:                       dataStore,
		Metrics:                     metrics.New(db),
		Temporal:                    temporalClient,
		TemporalTaskQueue:           cfg.TemporalTaskQueue,
		Questions:                   questions,
		SecureCookies:               cfg.Env != "dev",
		Billing:                     billingProvider,
		Reconciler:                  reconciler,
		StripePriceIDs:              cfg.StripePriceIDs,
		AppBaseURL:                  cfg.AppBaseURL,
		Webhooks:                    webhookVerifier,
		StripePortalConfigurationID: cfg.StripePortalConfigurationID,
		GoogleAuth:                  googleAuth,
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           apiServer.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info(
			"serve: starting",
			"mode", "serve",
			"http_addr", cfg.HTTPAddr,
			"env", cfg.Env,
			"prompt_runner_mode", cfg.PromptRunnerMode,
		)

		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}

		select {
		case err := <-errCh:
			return err
		case <-shutdownCtx.Done():
			return shutdownCtx.Err()
		}
	case err := <-errCh:
		return err
	}
}

// work connects to Temporal, registers the workflows and their activities, and
// runs the worker until shutdown.
func work(ctx context.Context, cfg config.Config) error {
	if ctx.Err() != nil {
		return nil
	}

	db, err := store.Open(ctx, cfg.DatabaseURL, int32(cfg.DBMaxOpenConns))
	if err != nil {
		return err
	}
	defer db.Close()

	runner, err := llm.NewPromptRunner(string(cfg.PromptRunnerMode), llm.OpenAIConfig{
		APIKey: cfg.OpenAIAPIKey,
		Model:  cfg.OpenAIResponsesModel,
	})
	if err != nil {
		return fmt.Errorf("build prompt runner: %w", err)
	}

	extractor, err := llm.NewExtractionRunner(string(cfg.PromptRunnerMode), llm.OpenAIConfig{
		APIKey: cfg.OpenAIAPIKey,
		Model:  cfg.OpenAIAnalysisModel,
	})
	if err != nil {
		return fmt.Errorf("build extraction runner: %w", err)
	}

	matcher, err := llm.NewMatchRunner(string(cfg.PromptRunnerMode), llm.OpenAIConfig{
		APIKey: cfg.OpenAIAPIKey,
		Model:  cfg.OpenAIAnalysisModel,
	})
	if err != nil {
		return fmt.Errorf("build match runner: %w", err)
	}

	proposer, err := llm.NewProposeProfileRunner(string(cfg.PromptRunnerMode), llm.OpenAIConfig{
		APIKey: cfg.OpenAIAPIKey,
		Model:  cfg.OpenAIOnboardingModel,
	})
	if err != nil {
		return fmt.Errorf("build propose profile runner: %w", err)
	}

	temporalClient, err := dialTemporal(ctx, cfg)
	if err != nil {
		return err
	}
	defer temporalClient.Close()

	slog.Info(
		"work: connected to temporal",
		"mode", "work",
		"temporal_address", cfg.TemporalAddress,
		"temporal_namespace", cfg.TemporalNamespace,
		"temporal_task_queue", cfg.TemporalTaskQueue,
	)

	activities := &workflows.Activities{
		Store:     store.New(db),
		Runner:    runner,
		Extractor: extractor,
		Matcher:   matcher,
		Proposer:  proposer,
	}

	w := worker.New(temporalClient, cfg.TemporalTaskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize: cfg.PromptConcurrency,
	})
	w.RegisterWorkflow(workflows.RunWorkflow)
	w.RegisterWorkflow(workflows.AnalyzeRun)
	w.RegisterWorkflow(workflows.GenerateProfileWorkflow)
	w.RegisterActivity(activities.CheckRunAccess)
	w.RegisterActivity(activities.LoadRunSpec)
	w.RegisterActivity(activities.ExecutePrompt)
	w.RegisterActivity(activities.FinalizeRun)
	w.RegisterActivity(activities.FetchSite)
	w.RegisterActivity(activities.AnalyzeResult)
	w.RegisterActivity(activities.LoadAnalyzeRunSpec)
	w.RegisterActivity(activities.ReconcileEntities)
	w.RegisterActivity(activities.ProposeProfile)
	w.RegisterActivity(activities.PersistProposal)

	if err := w.Start(); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}
	defer w.Stop()

	slog.Info(
		"work: worker started",
		"mode", "work",
		"temporal_task_queue", cfg.TemporalTaskQueue,
		"prompt_concurrency", cfg.PromptConcurrency,
		"prompt_runner_mode", cfg.PromptRunnerMode,
	)

	<-ctx.Done()
	return nil
}

// validateServeRuntimeConfig is serve-specific: migrations, workers and
// operator commands should not require credentials for services they do not
// use. The portal configuration command validates its own smaller Stripe
// subset, including the same pre-provisioned configuration id serve requires.
func validateServeRuntimeConfig(cfg config.Config) error {
	if cfg.StripeSecretKey == "" {
		return errors.New("STRIPE_SECRET_KEY is required to serve")
	}
	if cfg.StripeWebhookSecret == "" {
		return errors.New("STRIPE_WEBHOOK_SECRET is required to serve")
	}
	if cfg.StripePortalConfigurationID == "" {
		return errors.New("STRIPE_PORTAL_CONFIGURATION_ID is required to serve")
	}
	if cfg.AppBaseURL == "" {
		return errors.New("APP_BASE_URL is required to serve")
	}
	for _, plan := range billing.Plans() {
		if cfg.StripePriceIDs[plan.Code] == "" {
			return fmt.Errorf("%s is required to serve", plan.PriceEnvKey)
		}
	}
	if cfg.GoogleClientID == "" {
		return errors.New("GOOGLE_CLIENT_ID is required to serve")
	}
	if cfg.GoogleClientSecret == "" {
		return errors.New("GOOGLE_CLIENT_SECRET is required to serve")
	}
	return nil
}

// dialTemporal connects to the Temporal frontend with a bounded dial timeout.
// Callers own closing the returned client.
func dialTemporal(ctx context.Context, cfg config.Config) (client.Client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	temporalClient, err := client.DialContext(dialCtx, client.Options{
		HostPort:  cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		return nil, fmt.Errorf("connect temporal: %w", err)
	}
	return temporalClient, nil
}

// migrate applies embedded goose migrations and exits. It is intentionally only
// called by the explicit migrate subcommand, never by serve or work startup.
func migrate(ctx context.Context, cfg config.Config) error {
	applied, err := store.Migrate(ctx, store.MigrationConfig{
		DatabaseURL:  cfg.DatabaseURL,
		MaxOpenConns: cfg.DBMaxOpenConns,
	})
	if err != nil {
		return err
	}

	slog.Info(
		"migrate: complete",
		"mode", "migrate",
		"migrations_applied", applied,
		"db_max_open_conns", cfg.DBMaxOpenConns,
	)
	return nil
}
