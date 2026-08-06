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
	"opensight/internal/visibility"
)

type PromptRunnerMode string

const (
	PromptRunnerStub   PromptRunnerMode = "stub"
	PromptRunnerReplay PromptRunnerMode = "replay"
	PromptRunnerOpenAI PromptRunnerMode = "openai"
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
	defaultOnboardingModel   = "gpt-5.6-terra"
	// defaultQuestionsModel is deliberately a cheap non-reasoning model: customer
	// question generation does no research (design 03), unlike ProposeProfile's
	// reasoning model above.
	defaultQuestionsModel    = "gpt-5-mini"
	defaultPromptRunnerMode  = PromptRunnerStub
	defaultPromptConcurrency = 2
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
	OpenAIQuestionsModel  string
	PromptRunnerMode      PromptRunnerMode
	PromptConcurrency     int
	StripeSecretKey       string
	StripeWebhookSecret   string
	// StripePortalConfigurationID pins BillingService.CreatePortalSession to
	// the repo-owned Billing Portal Configuration (design 08 "Customer
	// Portal", internal/billing/portal.go) rather than the account default,
	// applied by `opensight stripe portal-config`.
	StripePortalConfigurationID string
	AppBaseURL                  string
	// StripePriceIDs maps a catalog plan code to its Stripe Price id,
	// resolved by reading each billing.Plan's PriceEnvKey (design 08 "the
	// pairing is one Go value"). A plan added to the catalog gets a required
	// env var here for free.
	StripePriceIDs map[string]string
	// GoogleClientID and GoogleClientSecret authenticate the Google OAuth
	// client behind /auth/google/* (design 07 "Auth and accounts"). The
	// redirect URI is derived from AppBaseURL, not configured separately.
	GoogleClientID     string
	GoogleClientSecret string
	AssessmentModes    map[string]visibility.RolloutMode
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

	promptConcurrency, err := getenvPositiveInt(getenv, "PROMPT_CONCURRENCY", defaultPromptConcurrency)
	if err != nil {
		return Config{}, err
	}

	appBaseURL := getenv("APP_BASE_URL")
	if appBaseURL != "" {
		var err error
		appBaseURL, err = normalizeAppBaseURL(appBaseURL)
		if err != nil {
			return Config{}, err
		}
	}

	stripeSecretKey := getenv("STRIPE_SECRET_KEY")
	stripeWebhookSecret := getenv("STRIPE_WEBHOOK_SECRET")
	stripePortalConfigurationID := getenv("STRIPE_PORTAL_CONFIGURATION_ID")
	assessmentModes, err := parseAssessmentModes(getenv("VISIBILITY_ASSESSOR_MODES"))
	if err != nil {
		return Config{}, err
	}

	// Built by iterating the catalog, not hardcoded to Starter, so a plan
	// added later is required in config for free (design 08 "the pairing is
	// one Go value").
	stripePriceIDs := make(map[string]string)
	for _, plan := range billing.Plans() {
		stripePriceIDs[plan.Code] = getenv(plan.PriceEnvKey)
	}

	return Config{
		Env:                         getenvString(getenv, "OPENSIGHT_ENV", defaultEnv),
		HTTPAddr:                    getenvString(getenv, "HTTP_ADDR", defaultHTTPAddr),
		DatabaseURL:                 getenvString(getenv, "DATABASE_URL", defaultDatabaseURL),
		DBMaxOpenConns:              dbMaxOpenConns,
		TemporalAddress:             getenvString(getenv, "TEMPORAL_ADDRESS", defaultTemporalAddress),
		TemporalNamespace:           getenvString(getenv, "TEMPORAL_NAMESPACE", defaultTemporalNamespace),
		TemporalTaskQueue:           getenvString(getenv, "TEMPORAL_TASK_QUEUE", defaultTemporalTaskQueue),
		OpenAIAPIKey:                getenv("OPENAI_API_KEY"),
		OpenAIResponsesModel:        getenvString(getenv, "OPENAI_RESPONSES_MODEL", defaultResponsesModel),
		OpenAIAnalysisModel:         getenvString(getenv, "OPENAI_ANALYSIS_MODEL", defaultAnalysisModel),
		OpenAIOnboardingModel:       getenvString(getenv, "OPENAI_ONBOARDING_MODEL", defaultOnboardingModel),
		OpenAIQuestionsModel:        getenvString(getenv, "OPENAI_QUESTIONS_MODEL", defaultQuestionsModel),
		PromptRunnerMode:            mode,
		PromptConcurrency:           promptConcurrency,
		StripeSecretKey:             stripeSecretKey,
		StripeWebhookSecret:         stripeWebhookSecret,
		StripePortalConfigurationID: stripePortalConfigurationID,
		AppBaseURL:                  appBaseURL,
		StripePriceIDs:              stripePriceIDs,
		GoogleClientID:              getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:          getenv("GOOGLE_CLIENT_SECRET"),
		AssessmentModes:             assessmentModes,
	}, nil
}

func parseAssessmentModes(raw string) (map[string]visibility.RolloutMode, error) {
	modes := map[string]visibility.RolloutMode{
		"search-access":      visibility.RolloutActive,
		"influential-source": visibility.RolloutShadow,
		"tracked-topic":      visibility.RolloutShadow,
	}
	if strings.TrimSpace(raw) == "" {
		return modes, nil
	}
	known := map[string]bool{}
	for _, assessor := range visibility.Assessors(modes) {
		known[assessor.Manifest().Key] = true
	}
	for _, item := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 || !known[parts[0]] {
			return nil, fmt.Errorf("VISIBILITY_ASSESSOR_MODES contains unknown assessor %q", parts[0])
		}
		mode := visibility.RolloutMode(strings.ToUpper(parts[1]))
		switch mode {
		case visibility.RolloutDisabled, visibility.RolloutShadow, visibility.RolloutActive:
			modes[parts[0]] = mode
		default:
			return nil, fmt.Errorf("VISIBILITY_ASSESSOR_MODES has invalid mode %q", parts[1])
		}
	}
	return modes, nil
}

func validPromptRunnerMode(mode PromptRunnerMode) bool {
	switch mode {
	case PromptRunnerStub, PromptRunnerReplay, PromptRunnerOpenAI:
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
