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
		"practitioners jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(practitioners) = 'array')",
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

func TestTenancyMigrationCreatesTablesAndStarterPlan(t *testing.T) {
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
