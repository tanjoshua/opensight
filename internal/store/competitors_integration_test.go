package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCompetitorStoreAccountScopingAndHistoryPreservation(t *testing.T) {
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run store integration tests")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)

	accountID := mustNewID(t)
	otherAccountID := mustNewID(t)
	businessID := mustNewID(t)
	promptID := mustNewID(t)
	runID := mustNewID(t)
	resultID := mustNewID(t)
	mentionID := mustNewID(t)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM businesses WHERE id = $1", businessID)
		_, _ = db.Exec(ctx, "DELETE FROM subscriptions WHERE account_id IN ($1, $2)", accountID, otherAccountID)
		_, _ = db.Exec(ctx, "DELETE FROM accounts WHERE id IN ($1, $2)", accountID, otherAccountID)
	})

	insertAccount(t, db, ctx, accountID, "Owner")
	insertAccount(t, db, ctx, otherAccountID, "Other")
	mustExec(t, db, ctx, `
		INSERT INTO businesses (id, account_id, status, name, category, location, activated_at)
		VALUES ($1, $2, 'active', 'Owner Clinic', 'clinic', '{"country":"SG"}', now())`, businessID, accountID)

	competitors := New(db)
	created, err := competitors.CreateManual(ctx, CreateManualCompetitorParams{
		AccountID: accountID, BusinessID: businessID, Name: "Rival Clinic",
		Aliases: []string{"Rival", " Rival "},
	})
	if err != nil {
		t.Fatalf("create manual competitor: %v", err)
	}
	if created.Source != "manual" || created.Status != CompetitorStatusTracked || len(created.Aliases) != 1 {
		t.Fatalf("created = %+v", created)
	}
	if _, err := competitors.CreateManual(ctx, CreateManualCompetitorParams{
		AccountID: otherAccountID, BusinessID: businessID, Name: "Leaked",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account create error = %v, want ErrNotFound", err)
	}

	mustExec(t, db, ctx, `
		INSERT INTO prompts (id, business_id, text, status)
		VALUES ($1, $2, 'best clinic', 'active')`, promptID, businessID)
	mustExec(t, db, ctx, `
		INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
		VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-20', 'completed', 'competitor-history', now(), now())`, runID, businessID)
	mustExec(t, db, ctx, `
		INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text, requested_at, completed_at)
		VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{}', '{}', 'text', now(), now())`, resultID, runID, promptID)
	mustExec(t, db, ctx, `
		INSERT INTO result_analyses (prompt_result_id, analysis_model, extraction_version)
		VALUES ($1, 'gpt-5-mini', 1)`, resultID)
	mustExec(t, db, ctx, `
		INSERT INTO mentions (id, prompt_result_id, subject, competitor_id, matched_by, mention_order, excerpt)
		VALUES ($1, $2, 'competitor', $3, 'exact', 0, 'Rival Clinic')`, mentionID, resultID, created.ID)

	err = New(db).CommitReconcile(ctx, accountID, businessID, ReconcileCommitParams{
		RunID: runID,
		SuggestedAliases: []SuggestedAliasWrite{
			{CompetitorID: created.ID, Variant: "  Rival Medical  "},
			{CompetitorID: created.ID, Variant: " Reject Me "},
			{CompetitorID: created.ID, Variant: "   "},
		},
		Mentions: []MentionWrite{{
			PromptResultID: resultID,
			Subject:        "competitor",
			CompetitorID:   created.ID,
			MatchedBy:      "exact",
			MentionOrder:   0,
			VerbatimName:   "Rival Clinic",
			Excerpt:        "Rival Clinic",
		}},
	})
	if err != nil {
		t.Fatalf("commit whitespace-bearing suggestions: %v", err)
	}
	approved, err := competitors.ApproveSuggestedAlias(ctx, SuggestedAliasParams{
		AccountID: accountID, CompetitorID: created.ID, Alias: "Rival Medical",
	})
	if err != nil {
		t.Fatalf("approve normalized suggested alias: %v", err)
	}
	if len(approved.Aliases) != 2 || approved.Aliases[1] != "Rival Medical" {
		t.Fatalf("approved aliases = %#v", approved.Aliases)
	}
	if len(approved.SuggestedAliases) != 1 || approved.SuggestedAliases[0] != "Reject Me" {
		t.Fatalf("normalized suggestions after approve = %#v", approved.SuggestedAliases)
	}
	rejected, err := competitors.RejectSuggestedAlias(ctx, SuggestedAliasParams{
		AccountID: accountID, CompetitorID: created.ID, Alias: "Reject Me",
	})
	if err != nil {
		t.Fatalf("reject normalized suggested alias: %v", err)
	}
	if len(rejected.SuggestedAliases) != 0 || len(rejected.Aliases) != 2 {
		t.Fatalf("record after reject = %+v", rejected)
	}
	if _, err := competitors.RejectSuggestedAlias(ctx, SuggestedAliasParams{
		AccountID: accountID, CompetitorID: created.ID, Alias: "Reject Me",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale reject error = %v, want ErrNotFound", err)
	}
	mustExec(t, db, ctx, `
		UPDATE competitors
		SET suggested_aliases = ARRAY['Private Alias']::text[]
		WHERE id = $1`, created.ID)
	if _, err := competitors.ApproveSuggestedAlias(ctx, SuggestedAliasParams{
		AccountID: otherAccountID, CompetitorID: created.ID, Alias: "Private Alias",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account approve error = %v, want ErrNotFound", err)
	}
	aliasesUpdated, err := competitors.UpdateAliases(ctx, UpdateCompetitorAliasesParams{
		AccountID: accountID, CompetitorID: created.ID,
		Aliases: []string{" Rival ", "rival", "Rival Medical"},
	})
	if err != nil {
		t.Fatalf("update approved aliases: %v", err)
	}
	if len(aliasesUpdated.Aliases) != 2 || aliasesUpdated.Aliases[0] != "Rival" ||
		len(aliasesUpdated.SuggestedAliases) != 1 || aliasesUpdated.SuggestedAliases[0] != "Private Alias" {
		t.Fatalf("aliases-only update changed wrong fields: %+v", aliasesUpdated)
	}
	if _, err := competitors.UpdateAliases(ctx, UpdateCompetitorAliasesParams{
		AccountID: otherAccountID, CompetitorID: created.ID, Aliases: []string{},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account aliases error = %v, want ErrNotFound", err)
	}

	if _, err := competitors.SetStatus(ctx, SetCompetitorStatusParams{
		AccountID: accountID, CompetitorID: created.ID, Status: CompetitorStatusDismissed,
	}); err != nil {
		t.Fatalf("dismiss competitor: %v", err)
	}
	restored, err := competitors.SetStatus(ctx, SetCompetitorStatusParams{
		AccountID: accountID, CompetitorID: created.ID, Status: CompetitorStatusTracked,
	})
	if err != nil {
		t.Fatalf("re-track competitor: %v", err)
	}
	if restored.Status != CompetitorStatusTracked {
		t.Fatalf("restored status = %q", restored.Status)
	}
	if _, err := competitors.SetStatus(ctx, SetCompetitorStatusParams{
		AccountID: otherAccountID, CompetitorID: created.ID, Status: CompetitorStatusDismissed,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account status error = %v, want ErrNotFound", err)
	}

	var mentions int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM mentions WHERE competitor_id = $1", created.ID).Scan(&mentions); err != nil {
		t.Fatalf("count mentions: %v", err)
	}
	if mentions != 1 {
		t.Fatalf("mentions after dismiss/re-track = %d, want 1", mentions)
	}
}
