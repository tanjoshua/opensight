package store

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSimplifyEntityAttributionMigration(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run migration integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(admin.Close)

	openSchema := func(t *testing.T) *sql.DB {
		t.Helper()
		schema := "migration_" + strings.ReplaceAll(mustNewID(t).String(), "-", "")
		if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
			t.Fatalf("create schema: %v", err)
		}
		t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
		u, err := url.Parse(dbURL)
		if err != nil {
			t.Fatalf("parse database URL: %v", err)
		}
		q := u.Query()
		q.Set("search_path", schema+",public")
		u.RawQuery = q.Encode()
		db, err := sql.Open("pgx", u.String())
		if err != nil {
			t.Fatalf("open schema database: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	t.Run("fresh schema", func(t *testing.T) {
		db := openSchema(t)
		provider, err := newMigrationProvider(db)
		if err != nil {
			t.Fatalf("migration provider: %v", err)
		}
		if _, err := provider.Up(ctx); err != nil {
			t.Fatalf("apply fresh migrations: %v", err)
		}
		assertAttributionSchema(t, ctx, db)
	})

	t.Run("upgrade wipes derived data and retains facts and configuration", func(t *testing.T) {
		db := openSchema(t)
		provider, err := newMigrationProvider(db)
		if err != nil {
			t.Fatalf("migration provider: %v", err)
		}
		if _, err := provider.UpTo(ctx, 18); err != nil {
			t.Fatalf("migrate to version 18: %v", err)
		}

		accountID, businessID, promptID := mustNewID(t), mustNewID(t), mustNewID(t)
		runID, resultID := mustNewID(t), mustNewID(t)
		competitorID := mustNewID(t)
		citationID, mentionID := mustNewID(t), mustNewID(t)
		auditID, findingID := mustNewID(t), mustNewID(t)
		mustSQLExec(t, ctx, db, `INSERT INTO accounts (id,name,slug) VALUES ($1,'Migration Account',$2)`, accountID, "migration-"+accountID.String())
		mustSQLExec(t, ctx, db, `INSERT INTO subscriptions (account_id,plan_code,comped) VALUES ($1,'starter',true)`, accountID)
		mustSQLExec(t, ctx, db, `INSERT INTO businesses (id,account_id,status,name) VALUES ($1,$2,'draft','Retained Business')`, businessID, accountID)
		mustSQLExec(t, ctx, db, `INSERT INTO prompts (id,business_id,text,status) VALUES ($1,$2,'retained prompt','active')`, promptID, businessID)
		mustSQLExec(t, ctx, db, `INSERT INTO competitors (id,business_id,name,source,status) VALUES ($1,$2,'Retained Rival','manual','tracked')`, competitorID, businessID)
		mustSQLExec(t, ctx, db, `INSERT INTO monitoring_runs (id,business_id,platform,trigger,scheduled_for,status,workflow_id,completed_at,analysis_completed_at) VALUES ($1,$2,'chatgpt','manual','2026-08-01','completed','retained-run',now(),now())`, runID, businessID)
		mustSQLExec(t, ctx, db, `INSERT INTO prompt_results (id,run_id,prompt_id,status,model,request,raw_response,response_text) VALUES ($1,$2,$3,'succeeded','retained-model','{}','{"retained":true}','Retained raw answer')`, resultID, runID, promptID)
		mustSQLExec(t, ctx, db, `INSERT INTO result_analyses (prompt_result_id,analysis_model,extraction_version) VALUES ($1,'old-model',4)`, resultID)
		mustSQLExec(t, ctx, db, `INSERT INTO citations (id,prompt_result_id,url,domain,cite_order,subject,text_start,text_end) VALUES ($1,$2,'https://example.com','example.com',0,'competitor',0,10)`, citationID, resultID)
		mustSQLExec(t, ctx, db, `INSERT INTO mentions (id,prompt_result_id,subject,competitor_id,matched_by,mention_order,verbatim_name,excerpt,citation_id) VALUES ($1,$2,'competitor',$3,'exact',0,'Retained Rival','Retained Rival',$4)`, mentionID, resultID, competitorID, citationID)
		mustSQLExec(t, ctx, db, `INSERT INTO site_audits (id,account_id,business_id,monitoring_run_id,published) VALUES ($1,$2,$3,$4,true)`, auditID, accountID, businessID, runID)
		mustSQLExec(t, ctx, db, `INSERT INTO findings (id,account_id,business_id,key,source,category,title,body) VALUES ($1,$2,$3,'old-finding','citation-gap','listings','Old','Old')`, findingID, accountID, businessID)

		if _, err := provider.Up(ctx); err != nil {
			t.Fatalf("apply attribution migration: %v", err)
		}
		assertAttributionSchema(t, ctx, db)
		for _, table := range []string{"findings", "site_audits", "mentions", "citations", "result_analyses"} {
			if got := sqlCount(t, ctx, db, "SELECT count(*) FROM "+table); got != 0 {
				t.Errorf("%s rows after migration = %d, want 0", table, got)
			}
		}
		for _, table := range []string{"accounts", "businesses", "prompts", "competitors"} {
			if got := sqlCount(t, ctx, db, "SELECT count(*) FROM "+table); got != 1 {
				t.Errorf("%s rows after migration = %d, want 1", table, got)
			}
		}
		for _, table := range []string{"monitoring_runs", "prompt_results"} {
			if got := sqlCount(t, ctx, db, "SELECT count(*) FROM "+table); got != 0 {
				t.Errorf("%s rows after cutover = %d, want 0", table, got)
			}
		}
	})
}

func assertAttributionSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if got := sqlCount(t, ctx, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='mention_citations'`); got != 1 {
		t.Fatalf("mention_citations table count = %d, want 1", got)
	}
	if got := sqlCount(t, ctx, db, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='mentions' AND column_name='citation_id'`); got != 0 {
		t.Fatalf("mentions.citation_id column count = %d, want 0", got)
	}
}

func mustSQLExec(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}

func sqlCount(t *testing.T, ctx context.Context, db *sql.DB, query string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", query, err)
	}
	return count
}
