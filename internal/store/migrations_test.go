package store

import (
	"io/fs"
	"strings"
	"testing"
)

func TestEmbeddedMigrationsIncludeInitialUpAndDown(t *testing.T) {
	names, err := fs.Glob(embeddedMigrations, "migrations/*.sql")
	if err != nil {
		t.Fatalf("glob embedded migrations: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("embedded migration count = %d, want 1 (%v)", len(names), names)
	}
	if names[0] != "migrations/00001_enable_citext.sql" {
		t.Fatalf("initial migration = %q, want migrations/00001_enable_citext.sql", names[0])
	}

	content, err := embeddedMigrations.ReadFile(names[0])
	if err != nil {
		t.Fatalf("read embedded migration: %v", err)
	}

	sql := string(content)
	for _, marker := range []string{"-- +goose Up", "-- +goose Down", "CREATE EXTENSION IF NOT EXISTS citext", "DROP EXTENSION IF EXISTS citext"} {
		if !strings.Contains(sql, marker) {
			t.Errorf("initial migration missing %q", marker)
		}
	}
}
