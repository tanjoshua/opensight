package store

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestEmbeddedMigrationsIncludeExpectedFiles(t *testing.T) {
	names, err := fs.Glob(embeddedMigrations, "migrations/*.sql")
	if err != nil {
		t.Fatalf("glob embedded migrations: %v", err)
	}
	want := []string{
		"migrations/00001_enable_citext.sql",
		"migrations/00002_create_plans_tenants_users.sql",
		"migrations/00003_create_business_profile_prompt_tables.sql",
		"migrations/00004_create_runs_results_tables.sql",
		"migrations/00005_add_password_auth_sessions.sql",
		"migrations/00006_create_analysis_tables.sql",
		"migrations/00007_add_mention_verbatim_name.sql",
		"migrations/00008_drop_business_practitioners.sql",
		"migrations/00009_add_run_expected_results.sql",
		"migrations/00010_create_subscriptions_stripe_events.sql",
		"migrations/00011_drop_stripe_events.sql",
		"migrations/00012_create_analyzed_results_view.sql",
		"migrations/00013_google_identity.sql",
		"migrations/00014_accounts_memberships.sql",
		"migrations/00015_create_visibility_assessments.sql",
		"migrations/00016_add_assessment_checks.sql",
		"migrations/00017_rebuild_improve.sql",
		"migrations/00018_attribute_citations.sql",
		"migrations/00019_simplify_entity_attribution.sql",
		"migrations/00020_drop_finding_verification.sql",
		"migrations/00021_add_finding_comparison.sql",
		"migrations/00022_replace_temporal_with_river.sql",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("embedded migrations = %v, want %v", names, want)
	}

	for _, name := range names {
		content, err := embeddedMigrations.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded migration %s: %v", name, err)
		}
		sql := string(content)
		for _, marker := range []string{"-- +goose Up", "-- +goose Down"} {
			if !strings.Contains(sql, marker) {
				t.Errorf("%s missing %q", name, marker)
			}
		}
	}
}

func TestDropStripeEventsMigration(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00011_drop_stripe_events.sql")
	if err != nil {
		t.Fatalf("read drop stripe_events migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"DROP TABLE stripe_events",
		"CREATE TABLE stripe_events",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("drop stripe_events migration missing %q", marker)
		}
	}
}

func TestRunsResultsMigrationCreatesAppendOnlyTables(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00004_create_runs_results_tables.sql")
	if err != nil {
		t.Fatalf("read runs/results migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"CREATE TABLE monitoring_runs",
		"business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE",
		"platform text NOT NULL CHECK (btrim(platform) <> '')",
		"trigger text NOT NULL CHECK (trigger IN ('initial', 'scheduled', 'manual'))",
		"scheduled_for date NOT NULL",
		"status text NOT NULL CHECK (status IN ('running', 'completed', 'partial', 'failed'))",
		"workflow_id text NOT NULL CHECK (btrim(workflow_id) <> '')",
		"analysis_completed_at timestamptz",
		"CONSTRAINT monitoring_runs_id_uuidv7 CHECK",
		"CONSTRAINT monitoring_runs_completion_check CHECK",
		"UNIQUE (business_id, platform, scheduled_for)",
		"CREATE TABLE prompt_results",
		"run_id uuid NOT NULL REFERENCES monitoring_runs(id) ON DELETE CASCADE",
		"prompt_id uuid NOT NULL REFERENCES prompts(id)",
		"status text NOT NULL CHECK (status IN ('succeeded', 'failed'))",
		"request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object')",
		"raw_response jsonb CHECK (raw_response IS NULL OR jsonb_typeof(raw_response) = 'object')",
		"response_text text CHECK (response_text IS NULL OR btrim(response_text) <> '')",
		"error text CHECK (error IS NULL OR btrim(error) <> '')",
		"CONSTRAINT prompt_results_id_uuidv7 CHECK",
		"CONSTRAINT prompt_results_payload_status_check CHECK",
		"UNIQUE (run_id, prompt_id)",
		"CREATE TRIGGER prompt_results_reject_update",
		"BEFORE UPDATE ON prompt_results",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("runs/results migration missing %q", marker)
		}
	}
}

func TestAnalysisMigrationCreatesDerivedTables(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00006_create_analysis_tables.sql")
	if err != nil {
		t.Fatalf("read analysis migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"CREATE TABLE result_analyses",
		"prompt_result_id uuid PRIMARY KEY REFERENCES prompt_results(id) ON DELETE CASCADE",
		"sentiment text CHECK (sentiment IS NULL OR sentiment IN ('positive', 'neutral', 'negative', 'mixed'))",
		"excerpts jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(excerpts) = 'array')",
		"extraction_version int NOT NULL",
		"CREATE TABLE competitors",
		"source text NOT NULL CHECK (source IN ('discovered', 'manual'))",
		"status text NOT NULL CHECK (status IN ('discovered', 'tracked', 'dismissed'))",
		"CONSTRAINT competitors_id_uuidv7 CHECK",
		"CREATE INDEX competitors_business_id_idx ON competitors (business_id)",
		"CREATE TABLE mentions",
		"subject text NOT NULL CHECK (subject IN ('self', 'competitor'))",
		"matched_by text NOT NULL CHECK (matched_by IN ('exact', 'llm'))",
		"CONSTRAINT mentions_competitor_id_subject_check CHECK",
		"CREATE INDEX mentions_prompt_result_id_idx ON mentions (prompt_result_id)",
		"CREATE INDEX mentions_competitor_id_idx ON mentions (competitor_id)",
		"CREATE TABLE citations",
		"subject text NOT NULL CHECK (subject IN ('business', 'competitor', 'other', 'unknown'))",
		"CREATE INDEX citations_prompt_result_id_idx ON citations (prompt_result_id)",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("analysis migration missing %q", marker)
		}
	}
}

func TestMentionVerbatimNameMigration(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00007_add_mention_verbatim_name.sql")
	if err != nil {
		t.Fatalf("read mention verbatim migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"ALTER TABLE mentions ADD COLUMN verbatim_name text",
		"UPDATE mentions SET verbatim_name = excerpt",
		"verbatim_name IS NULL OR btrim(verbatim_name) <> ''",
		"DROP COLUMN IF EXISTS verbatim_name",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("mention verbatim migration missing %q", marker)
		}
	}
}

// TestPasswordAuthSessionsMigration is a historical-content check: 00005 is
// what first created sessions (still current) and password_hash (later
// dropped by 00013 — see TestGoogleIdentityMigration below).
func TestPasswordAuthSessionsMigration(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00005_add_password_auth_sessions.sql")
	if err != nil {
		t.Fatalf("read auth migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"ALTER TABLE users ADD COLUMN password_hash text",
		"CREATE TABLE sessions",
		"token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32)",
		"user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE",
		"expires_at timestamptz NOT NULL",
		"CREATE INDEX sessions_user_id_idx ON sessions (user_id)",
		"CREATE INDEX sessions_expires_at_idx ON sessions (expires_at)",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("auth migration missing %q", marker)
		}
	}
}

// TestGoogleIdentityMigration covers 00013, which supersedes 00005's
// password_hash column with google_sub (design 07 "Auth and accounts").
func TestGoogleIdentityMigration(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00013_google_identity.sql")
	if err != nil {
		t.Fatalf("read google identity migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"ALTER TABLE users DROP COLUMN password_hash",
		"ALTER TABLE users ADD COLUMN google_sub text UNIQUE",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("google identity migration missing %q", marker)
		}
	}
}

func TestBusinessProfilePromptMigrationCreatesTables(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00003_create_business_profile_prompt_tables.sql")
	if err != nil {
		t.Fatalf("read business/profile/prompt migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"CREATE TABLE businesses",
		"tenant_id uuid NOT NULL REFERENCES tenants(id)",
		"status text NOT NULL CHECK (status IN ('draft', 'active'))",
		"aliases text[] NOT NULL DEFAULT ARRAY[]::text[]",
		"services jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(services) = 'array')",
		"location jsonb",
		"activated_at timestamptz",
		"CONSTRAINT businesses_id_uuidv7 CHECK",
		"CONSTRAINT businesses_active_profile_check CHECK",
		"CREATE TABLE profile_proposals",
		"payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object')",
		"status text NOT NULL CHECK (status IN ('pending', 'applied', 'discarded'))",
		"resolved_at timestamptz",
		"CONSTRAINT profile_proposals_id_uuidv7 CHECK",
		"CREATE TABLE prompts",
		"text text NOT NULL CHECK (btrim(text) <> '')",
		"status text NOT NULL CHECK (status IN ('active', 'retired'))",
		"replaces_prompt_id uuid REFERENCES prompts(id)",
		"retired_at timestamptz",
		"CONSTRAINT prompts_id_uuidv7 CHECK",
		"CONSTRAINT prompts_retirement_check CHECK",
		"CONSTRAINT prompts_no_self_replacement_check CHECK",
		"CREATE TRIGGER prompts_reject_text_update",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("business/profile/prompt migration missing %q", marker)
		}
	}
}

func TestInitialMigrationEnablesCITEXT(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00001_enable_citext.sql")
	if err != nil {
		t.Fatalf("read initial migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{"CREATE EXTENSION IF NOT EXISTS citext", "DROP EXTENSION IF EXISTS citext"} {
		if !strings.Contains(sql, marker) {
			t.Errorf("initial migration missing %q", marker)
		}
	}
}

func TestInitialAccountMigrationCreatesTablesAndStarterPlan(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00002_create_plans_tenants_users.sql")
	if err != nil {
		t.Fatalf("read tenancy migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"CREATE TABLE plans",
		"CONSTRAINT plans_id_uuidv7 CHECK",
		"slug text NOT NULL UNIQUE",
		"prompt_limit integer NOT NULL CHECK (prompt_limit > 0)",
		"run_interval text NOT NULL CHECK (btrim(run_interval) <> '')",
		"platforms text[] NOT NULL CHECK (cardinality(platforms) > 0)",
		"CREATE TABLE tenants",
		"CONSTRAINT tenants_id_uuidv7 CHECK",
		"plan_id uuid NOT NULL REFERENCES plans(id)",
		"CREATE INDEX tenants_plan_id_idx ON tenants (plan_id)",
		"CREATE TABLE users",
		"CONSTRAINT users_id_uuidv7 CHECK",
		"email citext NOT NULL UNIQUE",
		"CREATE INDEX users_tenant_id_idx ON users (tenant_id)",
		"INSERT INTO plans",
		"'starter'",
		"ARRAY['chatgpt']::text[]",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("tenancy migration missing %q", marker)
		}
	}

	starterID, err := uuid.Parse("01950000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatalf("parse starter plan ID: %v", err)
	}
	if starterID.Version() != uuid.Version(7) {
		t.Fatalf("starter plan ID version = %s, want VERSION_7", starterID.Version())
	}
}

func TestAnalyzedResultsViewMigration(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00012_create_analyzed_results_view.sql")
	if err != nil {
		t.Fatalf("read analyzed_results view migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"CREATE VIEW analyzed_results AS",
		"JOIN monitoring_runs r ON r.id = pr.run_id",
		"JOIN businesses b ON b.id = r.business_id",
		"JOIN result_analyses ra ON ra.prompt_result_id = pr.id",
		"WHERE r.analysis_completed_at IS NOT NULL",
		"DROP VIEW IF EXISTS analyzed_results",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("analyzed_results view migration missing %q", marker)
		}
	}
}

func TestSubscriptionsStripeEventsMigrationDropsPlans(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00010_create_subscriptions_stripe_events.sql")
	if err != nil {
		t.Fatalf("read subscriptions/stripe_events migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{
		"CREATE TABLE subscriptions",
		"tenant_id uuid PRIMARY KEY REFERENCES tenants(id)",
		"plan_code text NOT NULL CHECK (btrim(plan_code) <> '')",
		"stripe_customer_id text UNIQUE",
		"stripe_subscription_id text UNIQUE",
		"comped boolean NOT NULL DEFAULT false",
		"CREATE TABLE stripe_events",
		"id text PRIMARY KEY CHECK (btrim(id) <> '')",
		"payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object')",
		"INSERT INTO subscriptions (tenant_id, plan_code, comped)",
		"SELECT id, 'starter', true FROM tenants",
		"ALTER TABLE tenants DROP COLUMN plan_id",
		"DROP TABLE plans",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("subscriptions/stripe_events migration missing %q", marker)
		}
	}
}

func TestAccountsMembershipsMigration(t *testing.T) {
	content, err := embeddedMigrations.ReadFile("migrations/00014_accounts_memberships.sql")
	if err != nil {
		t.Fatalf("read accounts migration: %v", err)
	}
	sql := string(content)
	for _, marker := range []string{
		"ALTER TABLE tenants RENAME TO accounts",
		"ALTER TABLE businesses RENAME COLUMN tenant_id TO account_id",
		"ALTER TABLE subscriptions RENAME COLUMN tenant_id TO account_id",
		"CREATE TABLE account_memberships",
		"PRIMARY KEY (account_id, user_id)",
		"SELECT tenant_id, id, 'owner' FROM users",
		"ALTER TABLE users DROP COLUMN tenant_id",
		"every user must have exactly one account membership",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("accounts migration missing %q", marker)
		}
	}
}
