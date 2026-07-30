// Package config loads runtime settings from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"opensight/internal/billing"
)

type PromptRunnerMode string

const (
	PromptRunnerStub   PromptRunnerMode = "stub"
	PromptRunnerReplay PromptRunnerMode = "replay"
	PromptRunnerOpenAI PromptRunnerMode = "openai"
)

// BillingProvider selects the billing.Provider implementation (design 08
// "Local development"), the same stub/real split as PromptRunnerMode.
type BillingProvider string

const (
	BillingProviderStub   BillingProvider = "stub"
	BillingProviderStripe BillingProvider = "stripe"
)

const (
	defaultEnv               = "dev"
	defaultHTTPAddr          = ":8080"
	defaultDatabaseURL       = "postgres://opensight:opensight@localhost:5432/opensight?sslmode=disable"
	defaultDBMaxOpenConns    = 10
	defaultTemporalAddress   = "localhost:7233"
	defaultTemporalNamespace = "default"
	defaultTemporalTaskQueue = "opensight"
	defaultResponsesModel    = "chat-latest"
	defaultAnalysisModel     = "gpt-5.6-luna"
	defaultOnboardingModel   = "gpt-5.6-luna"
	defaultPromptRunnerMode  = PromptRunnerStub
	defaultDevPromptLimit    = 3
	defaultPromptConcurrency = 2
	defaultBillingProvider   = BillingProviderStub
	// defaultAppBaseURL is the Vite dev server port (scripts/dev-up).
	defaultAppBaseURL = "http://localhost:5173"
)

type Config struct {
	Env                   string
	HTTPAddr              string
	DatabaseURL           string
	DBMaxOpenConns        int
	TemporalAddress       string
	TemporalNamespace     string
	TemporalTaskQueue     string
	OpenAIAPIKey          string
	OpenAIResponsesModel  string
	OpenAIAnalysisModel   string
	OpenAIOnboardingModel string
	PromptRunnerMode      PromptRunnerMode
	DevPromptLimit        int
	PromptConcurrency     int
	BillingProvider       BillingProvider
	StripeSecretKey       string
	StripeWebhookSecret   string
	AppBaseURL            string
	// StripePriceIDs maps a catalog plan code to its Stripe Price id,
	// resolved by reading each billing.Plan's PriceEnvKey (design 08 "the
	// pairing is one Go value"). A plan added to the catalog gets a required
	// env var here for free.
	StripePriceIDs map[string]string
}

// Load reads runtime settings from the process environment. It first loads a
// .env file from the working directory if present (dev convenience, e.g. air
// under `make dev-serve`/`dev-work` — see docs/dev.md), without overriding
// any variable already set in the real environment; a missing .env (the
// normal case in deployment) is not an error.
func Load() (Config, error) {
	_ = godotenv.Load()
	return LoadFromEnv(os.Getenv)
}

func LoadFromEnv(getenv func(string) string) (Config, error) {
	mode := PromptRunnerMode(getenvString(getenv, "PROMPT_RUNNER_MODE", string(defaultPromptRunnerMode)))
	if !validPromptRunnerMode(mode) {
		return Config{}, fmt.Errorf("PROMPT_RUNNER_MODE must be one of stub, replay, openai; got %q", mode)
	}

	dbMaxOpenConns, err := getenvPositiveInt(getenv, "APP_DB_MAX_OPEN_CONNS", defaultDBMaxOpenConns)
	if err != nil {
		return Config{}, err
	}

	devPromptLimit, err := getenvPositiveInt(getenv, "DEV_PROMPT_LIMIT", defaultDevPromptLimit)
	if err != nil {
		return Config{}, err
	}

	promptConcurrency, err := getenvPositiveInt(getenv, "PROMPT_CONCURRENCY", defaultPromptConcurrency)
	if err != nil {
		return Config{}, err
	}

	billingProvider := BillingProvider(getenvString(getenv, "BILLING_PROVIDER", string(defaultBillingProvider)))
	if !validBillingProvider(billingProvider) {
		return Config{}, fmt.Errorf("BILLING_PROVIDER must be one of stub, stripe; got %q", billingProvider)
	}

	rawAppBaseURL := getenv("APP_BASE_URL")
	appBaseURL, err := normalizeAppBaseURL(getenvString(getenv, "APP_BASE_URL", defaultAppBaseURL))
	if err != nil {
		return Config{}, err
	}

	stripeSecretKey := getenv("STRIPE_SECRET_KEY")
	stripeWebhookSecret := getenv("STRIPE_WEBHOOK_SECRET")

	// Built by iterating the catalog, not hardcoded to Starter, so a plan
	// added later is required in config for free (design 08 "the pairing is
	// one Go value").
	stripePriceIDs := make(map[string]string)
	for _, plan := range billing.Plans() {
		stripePriceIDs[plan.Code] = getenv(plan.PriceEnvKey)
	}

	if billingProvider == BillingProviderStripe {
		if stripeSecretKey == "" {
			return Config{}, fmt.Errorf("STRIPE_SECRET_KEY is required when BILLING_PROVIDER=stripe")
		}
		if stripeWebhookSecret == "" {
			return Config{}, fmt.Errorf("STRIPE_WEBHOOK_SECRET is required when BILLING_PROVIDER=stripe")
		}
		for _, plan := range billing.Plans() {
			if stripePriceIDs[plan.Code] == "" {
				return Config{}, fmt.Errorf("%s is required when BILLING_PROVIDER=stripe", plan.PriceEnvKey)
			}
		}
		// An explicit APP_BASE_URL, not the localhost default: silently
		// defaulting a production success_url would send paying customers
		// nowhere (design 08).
		if rawAppBaseURL == "" {
			return Config{}, fmt.Errorf("APP_BASE_URL is required when BILLING_PROVIDER=stripe")
		}
	}

	return Config{
		Env:                   getenvString(getenv, "OPENSIGHT_ENV", defaultEnv),
		HTTPAddr:              getenvString(getenv, "HTTP_ADDR", defaultHTTPAddr),
		DatabaseURL:           getenvString(getenv, "DATABASE_URL", defaultDatabaseURL),
		DBMaxOpenConns:        dbMaxOpenConns,
		TemporalAddress:       getenvString(getenv, "TEMPORAL_ADDRESS", defaultTemporalAddress),
		TemporalNamespace:     getenvString(getenv, "TEMPORAL_NAMESPACE", defaultTemporalNamespace),
		TemporalTaskQueue:     getenvString(getenv, "TEMPORAL_TASK_QUEUE", defaultTemporalTaskQueue),
		OpenAIAPIKey:          getenv("OPENAI_API_KEY"),
		OpenAIResponsesModel:  getenvString(getenv, "OPENAI_RESPONSES_MODEL", defaultResponsesModel),
		OpenAIAnalysisModel:   getenvString(getenv, "OPENAI_ANALYSIS_MODEL", defaultAnalysisModel),
		OpenAIOnboardingModel: getenvString(getenv, "OPENAI_ONBOARDING_MODEL", defaultOnboardingModel),
		PromptRunnerMode:      mode,
		DevPromptLimit:        devPromptLimit,
		PromptConcurrency:     promptConcurrency,
		BillingProvider:       billingProvider,
		StripeSecretKey:       stripeSecretKey,
		StripeWebhookSecret:   stripeWebhookSecret,
		AppBaseURL:            appBaseURL,
		StripePriceIDs:        stripePriceIDs,
	}, nil
}

func validPromptRunnerMode(mode PromptRunnerMode) bool {
	switch mode {
	case PromptRunnerStub, PromptRunnerReplay, PromptRunnerOpenAI:
		return true
	default:
		return false
	}
}

func validBillingProvider(provider BillingProvider) bool {
	switch provider {
	case BillingProviderStub, BillingProviderStripe:
		return true
	default:
		return false
	}
}

// normalizeAppBaseURL validates raw as an absolute http(s) URL with no query
// string, fragment, or userinfo (design 08 "Config and secrets") — any of
// those would otherwise survive into AppBaseURL and corrupt a path appended
// to it later (e.g. /checkout/return). It returns the string rebuilt from the
// parsed URL, with a trailing slash trimmed, so the guarantee is enforced by
// the parse rather than by accident of what the caller typed.
func normalizeAppBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("APP_BASE_URL must be an absolute http(s) URL with no query string, fragment, or userinfo; got %q", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String(), nil
}

func getenvString(getenv func(string) string, key, fallback string) string {
	value := getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getenvPositiveInt(getenv func(string) string, key string, fallback int) (int, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer: %w", key, err)
	}
	if value < 1 {
		return 0, fmt.Errorf("%s must be a positive integer; got %d", key, value)
	}
	return value, nil
}
