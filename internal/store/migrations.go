package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// MigrationConfig is the database subset required by the explicit migration
// command. It intentionally keeps migrations independent from app startup.
type MigrationConfig struct {
	DatabaseURL    string
	MaxOpenConns   int
	ConnectTimeout time.Duration
}

// Migrate applies all pending embedded migrations against the configured app
// database. It returns the number of migrations applied by this invocation.
func Migrate(ctx context.Context, cfg MigrationConfig) (int, error) {
	if ctx.Err() != nil {
		return 0, nil
	}

	if cfg.DatabaseURL == "" {
		return 0, fmt.Errorf("database URL is required")
	}
	if cfg.MaxOpenConns < 1 {
		return 0, fmt.Errorf("max open connections must be positive")
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return 0, fmt.Errorf("open database: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	db.SetMaxOpenConns(cfg.MaxOpenConns)

	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(connectCtx); err != nil {
		return 0, fmt.Errorf("connect database: %w", err)
	}

	provider, err := newMigrationProvider(db)
	if err != nil {
		return 0, err
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return 0, fmt.Errorf("apply migrations: %w", err)
	}
	return len(results), nil
}

func newMigrationProvider(db *sql.DB) (*goose.Provider, error) {
	migrations, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("load embedded migrations: %w", err)
	}

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrations,
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, nil
}
