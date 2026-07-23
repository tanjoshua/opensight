// Command opensight is the single OpenSight binary. Its mode is selected by
// subcommand:
//
//	opensight serve         # HTTP API server (01-D2)
//	opensight work          # Temporal worker (01-D2)
//	opensight migrate       # apply database migrations, then exit (07)
//	opensight tenant create # create an invite-only tenant (AUTH-2)
//	opensight user create   # create an invite-only user (AUTH-2)
//	opensight business create # seed an active business from a spec file (RUN-5)
//	opensight seed dev      # seed a dev tenant with replay results (RUN-2)
//
// The same image runs serve and work via a command override in deployment
// (07 "Deployment"). Foundation stories wire the first health server, Temporal
// connection, and migration stub; later stories add real routes, workflows, and
// migrations.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	"opensight/internal/auth"
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
	usage               = "usage: opensight <serve|work|migrate|tenant|user|business|seed>"
	tenantCreateUsage   = "usage: opensight tenant create --name <tenant-name>"
	userCreateUsage     = "usage: opensight user create --tenant <tenant-id> --email <email> [--password-stdin]"
	businessCreateUsage = "usage: opensight business create --tenant <tenant-id> --file <spec.yaml>"
	seedUsage           = "usage: opensight seed dev"
	accountPlanSlug     = "starter"
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

type commandDeps struct {
	migrate          func(context.Context, config.Config) error
	createTenant     func(context.Context, config.Config, tenantCreateOptions, io.Writer) error
	createUser       func(context.Context, config.Config, userCreateOptions, io.Writer) error
	createBusiness   func(context.Context, config.Config, businessCreateOptions, io.Writer) error
	seedDev          func(context.Context, config.Config, io.Writer) error
	generatePassword func() (string, error)
	stdin            io.Reader
	stdout           io.Writer
}

type tenantCreateOptions struct {
	Name string
}

type userCreateOptions struct {
	TenantID          domain.ID
	Email             string
	Password          string
	GeneratedPassword bool
}

// run dispatches the chosen subcommand. It is separated from main so it can be
// tested without spawning the process.
func run(ctx context.Context, args []string) error {
	return runWithDeps(ctx, args, defaultCommandDeps())
}

func runWithDeps(ctx context.Context, args []string, deps commandDeps) error {
	if len(args) == 0 {
		return fmt.Errorf("no subcommand given; %s", usage)
	}
	deps = deps.withDefaults()

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
		return deps.migrate(ctx, cfg)
	case "tenant":
		return runTenantCommand(ctx, cfg, args[1:], deps)
	case "user":
		return runUserCommand(ctx, cfg, args[1:], deps)
	case "business":
		return runBusinessCommand(ctx, cfg, args[1:], deps)
	case "seed":
		return runSeedCommand(ctx, cfg, args[1:], deps)
	default:
		return fmt.Errorf("unknown subcommand %q; %s", cmd, usage)
	}
}

func defaultCommandDeps() commandDeps {
	return commandDeps{
		migrate:          migrate,
		createTenant:     createTenantCLI,
		createUser:       createUserCLI,
		createBusiness:   createBusinessCLI,
		seedDev:          seedDevCLI,
		generatePassword: generatePassword,
		stdin:            os.Stdin,
		stdout:           os.Stdout,
	}
}

func (d commandDeps) withDefaults() commandDeps {
	defaults := defaultCommandDeps()
	if d.migrate == nil {
		d.migrate = defaults.migrate
	}
	if d.createTenant == nil {
		d.createTenant = defaults.createTenant
	}
	if d.createUser == nil {
		d.createUser = defaults.createUser
	}
	if d.createBusiness == nil {
		d.createBusiness = defaults.createBusiness
	}
	if d.seedDev == nil {
		d.seedDev = defaults.seedDev
	}
	if d.generatePassword == nil {
		d.generatePassword = defaults.generatePassword
	}
	if d.stdin == nil {
		d.stdin = defaults.stdin
	}
	if d.stdout == nil {
		d.stdout = defaults.stdout
	}
	return d
}

func runTenantCommand(ctx context.Context, cfg config.Config, args []string, deps commandDeps) error {
	if len(args) == 0 {
		return fmt.Errorf("tenant subcommand required; %s", tenantCreateUsage)
	}
	switch args[0] {
	case "create":
		opts, err := parseTenantCreateArgs(args[1:])
		if err != nil {
			return err
		}
		return deps.createTenant(ctx, cfg, opts, deps.stdout)
	default:
		return fmt.Errorf("unknown tenant subcommand %q; %s", args[0], tenantCreateUsage)
	}
}

func runUserCommand(ctx context.Context, cfg config.Config, args []string, deps commandDeps) error {
	if len(args) == 0 {
		return fmt.Errorf("user subcommand required; %s", userCreateUsage)
	}
	switch args[0] {
	case "create":
		opts, err := parseUserCreateArgs(args[1:], deps.generatePassword, deps.stdin)
		if err != nil {
			return err
		}
		return deps.createUser(ctx, cfg, opts, deps.stdout)
	default:
		return fmt.Errorf("unknown user subcommand %q; %s", args[0], userCreateUsage)
	}
}

func runBusinessCommand(ctx context.Context, cfg config.Config, args []string, deps commandDeps) error {
	if len(args) == 0 {
		return fmt.Errorf("business subcommand required; %s", businessCreateUsage)
	}
	switch args[0] {
	case "create":
		opts, err := parseBusinessCreateArgs(args[1:])
		if err != nil {
			return err
		}
		return deps.createBusiness(ctx, cfg, opts, deps.stdout)
	default:
		return fmt.Errorf("unknown business subcommand %q; %s", args[0], businessCreateUsage)
	}
}

func runSeedCommand(ctx context.Context, cfg config.Config, args []string, deps commandDeps) error {
	if len(args) == 0 {
		return fmt.Errorf("seed subcommand required; %s", seedUsage)
	}
	switch args[0] {
	case "dev":
		if len(args) > 1 {
			return fmt.Errorf("unexpected argument %q; %s", args[1], seedUsage)
		}
		return deps.seedDev(ctx, cfg, deps.stdout)
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

func parseUserCreateArgs(args []string, generate func() (string, error), stdin io.Reader) (userCreateOptions, error) {
	flags := flag.NewFlagSet("user create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tenantRaw := flags.String("tenant", "", "tenant id")
	email := flags.String("email", "", "user email")
	passwordStdin := flags.Bool("password-stdin", false, "read the initial password from stdin")
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

	opts := userCreateOptions{
		TenantID: tenantID,
		Email:    strings.TrimSpace(*email),
	}
	if *passwordStdin {
		opts.Password, err = readPasswordFromStdin(stdin)
		if err != nil {
			return userCreateOptions{}, err
		}
	} else {
		opts.GeneratedPassword = true
		opts.Password, err = generate()
		if err != nil {
			return userCreateOptions{}, fmt.Errorf("generate password: %w", err)
		}
		if opts.Password == "" {
			return userCreateOptions{}, errors.New("generate password: empty password")
		}
	}
	return opts, nil
}

func readPasswordFromStdin(in io.Reader) (string, error) {
	const maxStdinPasswordBytes = 2048

	raw, err := io.ReadAll(io.LimitReader(in, maxStdinPasswordBytes+1))
	if err != nil {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	if len(raw) > maxStdinPasswordBytes {
		return "", errors.New("password from stdin is too long")
	}

	password := strings.TrimRight(string(raw), "\r\n")
	if password == "" {
		return "", errors.New("password from stdin is required")
	}
	return password, nil
}

func createTenantCLI(ctx context.Context, cfg config.Config, opts tenantCreateOptions, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}

	admin, closeStore, err := openAdminStore(cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	tenant, err := admin.CreateTenant(ctx, store.CreateTenantParams{Name: opts.Name})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "tenant_id=%s\nname=%s\nplan=%s\n", tenant.ID, tenant.Name, accountPlanSlug)
	return err
}

func createUserCLI(ctx context.Context, cfg config.Config, opts userCreateOptions, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}

	passwordHash, err := auth.HashPassword(opts.Password)
	if err != nil {
		return err
	}

	admin, closeStore, err := openAdminStore(cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	user, err := admin.CreateUser(ctx, store.CreateUserParams{
		TenantID:     opts.TenantID,
		Email:        opts.Email,
		PasswordHash: passwordHash,
	})
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "user_id=%s\ntenant_id=%s\nemail=%s\n", user.ID, user.TenantID, user.Email); err != nil {
		return err
	}
	if opts.GeneratedPassword {
		_, err = fmt.Fprintf(out, "password=%s\n", opts.Password)
		return err
	}
	return nil
}

func openAdminStore(cfg config.Config) (*store.AdminStore, func(), error) {
	db, err := store.Open(cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		return nil, nil, err
	}
	return store.NewAdminStore(db), func() { _ = db.Close() }, nil
}

func generatePassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// serve runs the HTTP API server.
func serve(ctx context.Context, cfg config.Config) error {
	if ctx.Err() != nil {
		return nil
	}

	db, err := store.Open(cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
	}()

	// serve starts GenerateProfileWorkflow on the same task queue the worker
	// consumes (ONB-4), so it needs a Temporal client too.
	temporalClient, err := dialTemporal(ctx, cfg)
	if err != nil {
		return err
	}
	defer temporalClient.Close()

	// Secure cookies everywhere except plain-HTTP local dev (FND-2). Prod runs
	// behind Caddy TLS, where Secure must be set.
	apiServer := api.New(
		store.NewAuthStore(db),
		store.NewBusinessStore(db),
		store.NewAdminStore(db),
		store.NewProfileProposalStore(db),
		store.NewPromptStore(db),
		store.NewRunStore(db),
		store.NewResultStore(db),
		metrics.New(db),
		temporalClient,
		cfg.TemporalTaskQueue,
		cfg.Env != "dev",
	)

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

// work connects to Temporal, registers RunWorkflow and its activities, and runs
// the worker until shutdown (RUN-3).
func work(ctx context.Context, cfg config.Config) error {
	if ctx.Err() != nil {
		return nil
	}

	db, err := store.Open(cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
	}()

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
		Model:  cfg.OpenAIAnalysisModel,
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

	activities := workflows.NewActivities(
		store.NewBusinessStore(db),
		store.NewPromptStore(db),
		store.NewRunStore(db),
		store.NewResultStore(db),
		runner,
		store.NewAnalysisStore(db),
		extractor,
		matcher,
		proposer,
		store.NewProfileProposalStore(db),
	)

	w := worker.New(temporalClient, cfg.TemporalTaskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize: cfg.PromptConcurrency,
	})
	w.RegisterWorkflow(workflows.RunWorkflow)
	w.RegisterWorkflow(workflows.AnalyzeRun)
	w.RegisterWorkflow(workflows.GenerateProfileWorkflow)
	w.RegisterActivity(activities.LoadRunSpec)
	w.RegisterActivity(activities.ExecutePrompt)
	w.RegisterActivity(activities.FinalizeRun)
	w.RegisterActivity(activities.FetchSite)
	w.RegisterActivity(activities.AnalyzeResult)
	w.RegisterActivity(activities.LoadAnalyzeRunSpec)
	w.RegisterActivity(activities.ReconcileEntities)
	w.RegisterActivity(activities.ResearchBusiness)
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
		MaxIdleConns: cfg.DBMaxIdleConns,
	})
	if err != nil {
		return err
	}

	slog.Info(
		"migrate: complete",
		"mode", "migrate",
		"migrations_applied", applied,
		"db_max_open_conns", cfg.DBMaxOpenConns,
		"db_max_idle_conns", cfg.DBMaxIdleConns,
	)
	return nil
}
