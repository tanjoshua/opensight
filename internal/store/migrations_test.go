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
