// Package config loads runtime settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
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
	defaultDBMaxIdleConns    = 5
	defaultTemporalAddress   = "localhost:7233"
	defaultTemporalNamespace = "default"
	defaultTemporalTaskQueue = "opensight"
	defaultResponsesModel    = "chat-latest"
	defaultAnalysisModel     = "gpt-5.6-luna"
	defaultOnboardingModel   = "gpt-5.6-luna"
	defaultPromptRunnerMode  = PromptRunnerStub
	defaultDevPromptLimit    = 3
	defaultPromptConcurrency = 2
)

type Config struct {
	Env                   string
	HTTPAddr              string
	DatabaseURL           string
	DBMaxOpenConns        int
	DBMaxIdleConns        int
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
}

func Load() (Config, error) {
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

	dbMaxIdleConns, err := getenvPositiveInt(getenv, "APP_DB_MAX_IDLE_CONNS", defaultDBMaxIdleConns)
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

	return Config{
		Env:                   getenvString(getenv, "OPENSIGHT_ENV", defaultEnv),
		HTTPAddr:              getenvString(getenv, "HTTP_ADDR", defaultHTTPAddr),
		DatabaseURL:           getenvString(getenv, "DATABASE_URL", defaultDatabaseURL),
		DBMaxOpenConns:        dbMaxOpenConns,
		DBMaxIdleConns:        dbMaxIdleConns,
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
