package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Env != defaultEnv {
		t.Errorf("Env = %q, want %q", cfg.Env, defaultEnv)
	}
	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, defaultHTTPAddr)
	}
	if cfg.DatabaseURL != defaultDatabaseURL {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, defaultDatabaseURL)
	}
	if cfg.DBMaxOpenConns != defaultDBMaxOpenConns {
		t.Errorf("DBMaxOpenConns = %d, want %d", cfg.DBMaxOpenConns, defaultDBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != defaultDBMaxIdleConns {
		t.Errorf("DBMaxIdleConns = %d, want %d", cfg.DBMaxIdleConns, defaultDBMaxIdleConns)
	}
	if cfg.TemporalAddress != defaultTemporalAddress {
		t.Errorf("TemporalAddress = %q, want %q", cfg.TemporalAddress, defaultTemporalAddress)
	}
	if cfg.TemporalNamespace != defaultTemporalNamespace {
		t.Errorf("TemporalNamespace = %q, want %q", cfg.TemporalNamespace, defaultTemporalNamespace)
	}
	if cfg.TemporalTaskQueue != defaultTemporalTaskQueue {
		t.Errorf("TemporalTaskQueue = %q, want %q", cfg.TemporalTaskQueue, defaultTemporalTaskQueue)
	}
	if cfg.OpenAIAPIKey != "" {
		t.Errorf("OpenAIAPIKey = %q, want empty", cfg.OpenAIAPIKey)
	}
	if cfg.OpenAIResponsesModel != defaultResponsesModel {
		t.Errorf("OpenAIResponsesModel = %q, want %q", cfg.OpenAIResponsesModel, defaultResponsesModel)
	}
	if cfg.OpenAIAnalysisModel != defaultAnalysisModel {
		t.Errorf("OpenAIAnalysisModel = %q, want %q", cfg.OpenAIAnalysisModel, defaultAnalysisModel)
	}
	if cfg.PromptRunnerMode != defaultPromptRunnerMode {
		t.Errorf("PromptRunnerMode = %q, want %q", cfg.PromptRunnerMode, defaultPromptRunnerMode)
	}
	if cfg.DevPromptLimit != defaultDevPromptLimit {
		t.Errorf("DevPromptLimit = %d, want %d", cfg.DevPromptLimit, defaultDevPromptLimit)
	}
	if cfg.BillingProvider != BillingProviderStub {
		t.Errorf("BillingProvider = %q, want %q", cfg.BillingProvider, BillingProviderStub)
	}
	if cfg.AppBaseURL != defaultAppBaseURL {
		t.Errorf("AppBaseURL = %q, want %q", cfg.AppBaseURL, defaultAppBaseURL)
	}
	if cfg.StripeSecretKey != "" || cfg.StripeWebhookSecret != "" {
		t.Errorf("StripeSecretKey/StripeWebhookSecret = %q/%q, want empty", cfg.StripeSecretKey, cfg.StripeWebhookSecret)
	}
	if got := cfg.StripePriceIDs["starter"]; got != "" {
		t.Errorf(`StripePriceIDs["starter"] = %q, want empty`, got)
	}
}

func TestLoadOverrides(t *testing.T) {
	env := map[string]string{
		"OPENSIGHT_ENV":          "test",
		"HTTP_ADDR":              ":9090",
		"DATABASE_URL":           "postgres://example",
		"APP_DB_MAX_OPEN_CONNS":  "12",
		"APP_DB_MAX_IDLE_CONNS":  "6",
		"TEMPORAL_ADDRESS":       "temporal.example:7233",
		"TEMPORAL_NAMESPACE":     "opensight-dev",
		"TEMPORAL_TASK_QUEUE":    "opensight-test",
		"OPENAI_API_KEY":         "sk-test",
		"OPENAI_RESPONSES_MODEL": "gpt-5.6-luna",
		"OPENAI_ANALYSIS_MODEL":  "gpt-5.6-luna",
		"PROMPT_RUNNER_MODE":     "replay",
		"DEV_PROMPT_LIMIT":       "2",
		"PROMPT_CONCURRENCY":     "4",
	}

	cfg, err := LoadFromEnv(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Env != "test" {
		t.Errorf("Env = %q", cfg.Env)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.DatabaseURL != "postgres://example" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.DBMaxOpenConns != 12 {
		t.Errorf("DBMaxOpenConns = %d", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != 6 {
		t.Errorf("DBMaxIdleConns = %d", cfg.DBMaxIdleConns)
	}
	if cfg.TemporalAddress != "temporal.example:7233" {
		t.Errorf("TemporalAddress = %q", cfg.TemporalAddress)
	}
	if cfg.TemporalNamespace != "opensight-dev" {
		t.Errorf("TemporalNamespace = %q", cfg.TemporalNamespace)
	}
	if cfg.TemporalTaskQueue != "opensight-test" {
		t.Errorf("TemporalTaskQueue = %q", cfg.TemporalTaskQueue)
	}
	if cfg.OpenAIAPIKey != "sk-test" {
		t.Errorf("OpenAIAPIKey = %q", cfg.OpenAIAPIKey)
	}
	if cfg.OpenAIResponsesModel != "gpt-5.6-luna" {
		t.Errorf("OpenAIResponsesModel = %q", cfg.OpenAIResponsesModel)
	}
	if cfg.OpenAIAnalysisModel != "gpt-5.6-luna" {
		t.Errorf("OpenAIAnalysisModel = %q", cfg.OpenAIAnalysisModel)
	}
	if cfg.PromptRunnerMode != PromptRunnerReplay {
		t.Errorf("PromptRunnerMode = %q", cfg.PromptRunnerMode)
	}
	if cfg.DevPromptLimit != 2 {
		t.Errorf("DevPromptLimit = %d", cfg.DevPromptLimit)
	}
	if cfg.PromptConcurrency != 4 {
		t.Errorf("PromptConcurrency = %d", cfg.PromptConcurrency)
	}
}

func TestLoadRejectsInvalidPromptRunnerMode(t *testing.T) {
	env := map[string]string{"PROMPT_RUNNER_MODE": "live"}

	if _, err := LoadFromEnv(func(key string) string { return env[key] }); err == nil {
		t.Fatal("expected invalid PROMPT_RUNNER_MODE to return error")
	}
}

func TestLoadRejectsInvalidPositiveInt(t *testing.T) {
	env := map[string]string{"DEV_PROMPT_LIMIT": "0"}

	if _, err := LoadFromEnv(func(key string) string { return env[key] }); err == nil {
		t.Fatal("expected zero DEV_PROMPT_LIMIT to return error")
	}
}

func TestLoadRejectsInvalidBillingProvider(t *testing.T) {
	env := map[string]string{"BILLING_PROVIDER": "paypal"}

	if _, err := LoadFromEnv(func(key string) string { return env[key] }); err == nil {
		t.Fatal("expected invalid BILLING_PROVIDER to return error")
	}
}

func TestAppBaseURLNormalizedAndValidated(t *testing.T) {
	env := map[string]string{"APP_BASE_URL": "https://app.example.com/"}
	cfg, err := LoadFromEnv(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.AppBaseURL != "https://app.example.com" {
		t.Fatalf("AppBaseURL = %q, want trailing slash trimmed", cfg.AppBaseURL)
	}

	invalid := map[string]string{"APP_BASE_URL": "not-a-url"}
	if _, err := LoadFromEnv(func(key string) string { return invalid[key] }); err == nil {
		t.Fatal("expected non-absolute APP_BASE_URL to return error")
	}
}

// TestAppBaseURLRejectsSurvivingComponents guards against a query string,
// fragment, or userinfo silently surviving into AppBaseURL and corrupting a
// path appended to it later (e.g. /checkout/return).
func TestAppBaseURLRejectsSurvivingComponents(t *testing.T) {
	cases := map[string]string{
		"fragment": "https://app.example.com/#fragment",
		"query":    "https://app.example.com/?next=/billing",
		"userinfo": "https://user:pass@app.example.com",
	}

	for name, value := range cases {
		env := map[string]string{"APP_BASE_URL": value}
		if _, err := LoadFromEnv(func(key string) string { return env[key] }); err == nil {
			t.Fatalf("%s: APP_BASE_URL = %q: want error, got nil", name, value)
		}
	}
}

func TestStripePriceIDsResolveFromCatalog(t *testing.T) {
	env := map[string]string{"STRIPE_PRICE_STARTER_MONTHLY": "price_abc"}
	cfg, err := LoadFromEnv(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if got := cfg.StripePriceIDs["starter"]; got != "price_abc" {
		t.Fatalf(`StripePriceIDs["starter"] = %q, want price_abc`, got)
	}
}

// TestLoadStripeModeFailsFast covers design 08's fail-fast requirement: every
// var BILLING_PROVIDER=stripe needs must be present, one at a time, plus the
// success case with all of them set.
func TestLoadStripeModeFailsFast(t *testing.T) {
	complete := map[string]string{
		"BILLING_PROVIDER":             "stripe",
		"STRIPE_SECRET_KEY":            "sk_test_123",
		"STRIPE_WEBHOOK_SECRET":        "whsec_123",
		"STRIPE_PRICE_STARTER_MONTHLY": "price_123",
		"APP_BASE_URL":                 "https://app.example.com",
	}

	if _, err := LoadFromEnv(func(key string) string { return complete[key] }); err != nil {
		t.Fatalf("LoadFromEnv with every stripe var set: %v", err)
	}

	for missing := range complete {
		if missing == "BILLING_PROVIDER" {
			continue
		}
		env := make(map[string]string, len(complete))
		for k, v := range complete {
			env[k] = v
		}
		delete(env, missing)

		if _, err := LoadFromEnv(func(key string) string { return env[key] }); err == nil {
			t.Fatalf("LoadFromEnv with %s missing: want error, got nil", missing)
		}
	}
}
