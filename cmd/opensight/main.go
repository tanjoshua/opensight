// Command opensight is the single OpenSight binary. Its mode is selected by
// subcommand:
//
//	opensight serve           # HTTP API server (01-D2)
//	opensight migrate         # apply database migrations, then exit
//	opensight account create     # create an operator-provisioned account
//	opensight account member add # add a member to an account
//	opensight business create # seed an active business from a spec file
//	opensight seed dev        # seed a dev account and login
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
	"opensight/internal/jobs"
	"opensight/internal/llm"
	"opensight/internal/metrics"
	"opensight/internal/store"
	"opensight/internal/workflows"

	"github.com/google/uuid"
	river "github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

const (
	usage                 = "usage: opensight <serve|migrate|run|account|business|seed|stripe>"
	accountCreateUsage    = "usage: opensight account create --name <account-name>"
	accountMemberAddUsage = "usage: opensight account member add --account <account-id> --email <email> --role <owner|admin|member|viewer>"
	businessCreateUsage   = "usage: opensight business create --account <account-id> --file <spec.yaml>"
	runReanalyzeUsage     = "usage: opensight run reanalyze --account <account-id> --run <run-id>"
	seedUsage             = seedDevArgsUsage
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

type accountCreateOptions struct {
	Name string
}

type accountMemberAddOptions struct {
	AccountID domain.ID
	Email     string
	Role      store.AccountRole
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
	case "migrate":
		return migrate(ctx, cfg)
	case "run":
		return runJobCommand(ctx, cfg, args[1:])
	case "account":
		return runAccountCommand(ctx, cfg, args[1:])
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

func runAccountCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("account subcommand required; %s", accountCreateUsage)
	}
	switch args[0] {
	case "create":
		opts, err := parseAccountCreateArgs(args[1:])
		if err != nil {
			return err
		}
		return createAccountCLI(ctx, cfg, opts, os.Stdout)
	case "member":
		if len(args) < 2 || args[1] != "add" {
			return fmt.Errorf("account member subcommand must be add; %s", accountMemberAddUsage)
		}
		opts, err := parseAccountMemberAddArgs(args[2:])
		if err != nil {
			return err
		}
		return addAccountMemberCLI(ctx, cfg, opts, os.Stdout)
	default:
		return fmt.Errorf("unknown account subcommand %q; %s", args[0], accountCreateUsage)
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

func runJobCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] != "reanalyze" {
		return fmt.Errorf("%s", runReanalyzeUsage)
	}
	flags := flag.NewFlagSet("run reanalyze", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	accountRaw := flags.String("account", "", "account id")
	runRaw := flags.String("run", "", "run id")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return fmt.Errorf("%s", runReanalyzeUsage)
	}
	accountID, err := uuid.Parse(strings.TrimSpace(*accountRaw))
	if err != nil {
		return fmt.Errorf("--account must be a UUID: %w", err)
	}
	runID, err := uuid.Parse(strings.TrimSpace(*runRaw))
	if err != nil {
		return fmt.Errorf("--run must be a UUID: %w", err)
	}
	db, err := store.Open(ctx, cfg.DatabaseURL, int32(cfg.DBMaxOpenConns))
	if err != nil {
		return err
	}
	defer db.Close()
	repository := store.New(db)
	spec, err := repository.LoadAnalysisJobSpec(ctx, accountID, runID)
	if err != nil {
		return err
	}
	client, err := river.NewClient(riverpgxv5.New(db), &river.Config{})
	if err != nil {
		return err
	}
	inserted, err := client.Insert(ctx, jobs.AnalyzeArgs{AccountID: accountID, BusinessID: spec.BusinessID, RunID: runID}, nil)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "job_id=%d\nrun_id=%s\n", inserted.Job.ID, runID)
	return err
}

func parseAccountCreateArgs(args []string) (accountCreateOptions, error) {
	flags := flag.NewFlagSet("account create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("name", "", "account name")
	if err := flags.Parse(args); err != nil {
		return accountCreateOptions{}, fmt.Errorf("%s", accountCreateUsage)
	}
	if flags.NArg() != 0 {
		return accountCreateOptions{}, fmt.Errorf("unexpected argument %q; %s", flags.Arg(0), accountCreateUsage)
	}

	opts := accountCreateOptions{Name: strings.TrimSpace(*name)}
	if opts.Name == "" {
		return accountCreateOptions{}, fmt.Errorf("--name is required; %s", accountCreateUsage)
	}
	return opts, nil
}

func parseAccountMemberAddArgs(args []string) (accountMemberAddOptions, error) {
	flags := flag.NewFlagSet("account member add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	accountRaw := flags.String("account", "", "account id")
	email := flags.String("email", "", "member email")
	roleRaw := flags.String("role", "", "account role")
	if err := flags.Parse(args); err != nil {
		return accountMemberAddOptions{}, fmt.Errorf("%s", accountMemberAddUsage)
	}
	if flags.NArg() != 0 {
		return accountMemberAddOptions{}, fmt.Errorf("unexpected argument %q; %s", flags.Arg(0), accountMemberAddUsage)
	}
	if strings.TrimSpace(*accountRaw) == "" {
		return accountMemberAddOptions{}, fmt.Errorf("--account is required; %s", accountMemberAddUsage)
	}
	if strings.TrimSpace(*email) == "" {
		return accountMemberAddOptions{}, fmt.Errorf("--email is required; %s", accountMemberAddUsage)
	}

	accountID, err := uuid.Parse(strings.TrimSpace(*accountRaw))
	if err != nil {
		return accountMemberAddOptions{}, fmt.Errorf("--account must be a UUID: %w", err)
	}
	var role store.AccountRole
	switch strings.TrimSpace(*roleRaw) {
	case string(store.AccountRoleOwner):
		role = store.AccountRoleOwner
	case string(store.AccountRoleAdmin):
		role = store.AccountRoleAdmin
	case string(store.AccountRoleMember):
		role = store.AccountRoleMember
	case string(store.AccountRoleViewer):
		role = store.AccountRoleViewer
	default:
		return accountMemberAddOptions{}, errors.New("--role must be owner, admin, member, or viewer")
	}

	return accountMemberAddOptions{AccountID: accountID, Email: strings.TrimSpace(*email), Role: role}, nil
}

func createAccountCLI(ctx context.Context, cfg config.Config, opts accountCreateOptions, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}

	account, closeStore, err := openAccountStore(cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	created, err := account.CreateOperatorAccount(ctx, store.CreateOperatorAccountParams{Name: opts.Name})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "account_id=%s\nname=%s\nplan=%s\n", created.ID, created.Name, billing.Starter.Code)
	return err
}

// addAccountMemberCLI creates or reuses a global user and grants access to the
// account. A user with no Google identity links it on first sign-in (design 07
// "Auth and accounts").
func addAccountMemberCLI(ctx context.Context, cfg config.Config, opts accountMemberAddOptions, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}

	account, closeStore, err := openAccountStore(cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	member, err := account.AddAccountMember(ctx, store.AddAccountMemberParams{
		AccountID: opts.AccountID,
		Email:     opts.Email,
		Role:      opts.Role,
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "user_id=%s\naccount_id=%s\nemail=%s\nrole=%s\n", member.UserID, member.AccountID, member.Email, member.Role)
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
	// Validate before opening the database so a bad
	// environment fails without touching any other service.
	if err := validateServeRuntimeConfig(cfg); err != nil {
		return err
	}

	db, err := store.Open(ctx, cfg.DatabaseURL, int32(cfg.DBMaxOpenConns))
	if err != nil {
		return err
	}
	defer db.Close()

	limiter := llm.NewLimiter(cfg.LLMConcurrency)
	runner, err := llm.NewPromptRunner(string(cfg.PromptRunnerMode), llm.OpenAIConfig{APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIResponsesModel})
	if err != nil {
		return fmt.Errorf("build prompt runner: %w", err)
	}
	extractor, err := llm.NewExtractionRunner(string(cfg.PromptRunnerMode), llm.OpenAIConfig{APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIAnalysisModel})
	if err != nil {
		return fmt.Errorf("build extraction runner: %w", err)
	}
	matcher, err := llm.NewMatchRunner(string(cfg.PromptRunnerMode), llm.OpenAIConfig{APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIAnalysisModel})
	if err != nil {
		return fmt.Errorf("build match runner: %w", err)
	}
	sourceClassifier, err := llm.NewSourceClassifier(string(cfg.PromptRunnerMode), llm.OpenAIConfig{APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIImproveModel})
	if err != nil {
		return fmt.Errorf("build source classifier: %w", err)
	}
	proposer, err := llm.NewProposeProfileRunner(string(cfg.PromptRunnerMode), llm.OpenAIConfig{APIKey: cfg.OpenAIAPIKey, Model: cfg.OpenAIOnboardingModel})
	if err != nil {
		return fmt.Errorf("build propose profile runner: %w", err)
	}

	// Stripe is the sole runtime billing provider. Local development uses a
	// Stripe sandbox; the in-memory provider is retained only as a test fake.
	billingProvider, err := stripe.New(stripe.Config{APIKey: cfg.StripeSecretKey})
	if err != nil {
		return err
	}

	// GenerateQuestions is synchronous but shares the same process limiter as jobs.
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
	reconciler := reconcile.New(dataStore, billingProvider, nil, dataStore, nil)
	operations := &workflows.Operations{Store: dataStore, Runner: runner, Extractor: extractor, Matcher: matcher,
		Proposer: proposer, SourceClassifier: sourceClassifier, Limiter: limiter}
	workers := river.NewWorkers()
	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Workers: workers,
		Queues: map[string]river.QueueConfig{
			jobs.QueueScheduler: {MaxWorkers: 1}, jobs.QueueOnboarding: {MaxWorkers: 2},
			jobs.QueueMonitoring: {MaxWorkers: 2}, jobs.QueueAnalysis: {MaxWorkers: 2}, jobs.QueueAssessment: {MaxWorkers: 1},
		},
		PeriodicJobs: []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(jobs.SweepInterval),
			func() (river.JobArgs, *river.InsertOpts) { return jobs.SweepArgs{}, nil }, &river.PeriodicJobOpts{ID: "scheduler-sweep", RunOnStart: true})},
	})
	if err != nil {
		return fmt.Errorf("build river client: %w", err)
	}
	jobs.AddWorkers(workers, dataStore, operations, riverClient, cfg.LLMConcurrency)
	if err := riverClient.Start(context.Background()); err != nil {
		return fmt.Errorf("start river: %w", err)
	}
	defer func() { _ = riverClient.StopAndCancel(context.Background()) }()

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
		Jobs:                        riverClient,
		Limiter:                     limiter,
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

		if err := server.Shutdown(shutdownCtx); err != nil {
			cancel()
			return err
		}
		cancel()

		drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := riverClient.Stop(drainCtx)
		drainCancel()
		if errors.Is(err, context.DeadlineExceeded) {
			err = riverClient.StopAndCancel(context.Background())
		}
		if err != nil {
			return fmt.Errorf("stop river: %w", err)
		}

		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	case err := <-errCh:
		return err
	}
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

// migrate applies application migrations first, then River's bundled schema.
func migrate(ctx context.Context, cfg config.Config) error {
	if ctx.Err() != nil {
		return nil
	}
	applied, err := store.Migrate(ctx, store.MigrationConfig{
		DatabaseURL:  cfg.DatabaseURL,
		MaxOpenConns: cfg.DBMaxOpenConns,
	})
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL, int32(cfg.DBMaxOpenConns))
	if err != nil {
		return err
	}
	defer db.Close()
	riverMigrator, err := rivermigrate.New(riverpgxv5.New(db), nil)
	if err != nil {
		return fmt.Errorf("create river migrator: %w", err)
	}
	riverResult, err := riverMigrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("apply river migrations: %w", err)
	}

	slog.Info(
		"migrate: complete",
		"mode", "migrate",
		"migrations_applied", applied,
		"river_migrations_applied", len(riverResult.Versions),
		"db_max_open_conns", cfg.DBMaxOpenConns,
	)
	return nil
}
