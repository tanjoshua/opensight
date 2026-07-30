package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	testdb "opensight/internal/store/testdb"
)

func TestCompetitorStoreTenantScopingAndHistoryPreservation(t *testing.T) {
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

	tenantID := mustNewID(t)
	otherTenantID := mustNewID(t)
	businessID := mustNewID(t)
	promptID := mustNewID(t)
	runID := mustNewID(t)
	resultID := mustNewID(t)
	mentionID := mustNewID(t)
	t.Cleanup(func() {
		_, _ = testdb.Exec(ctx, db, testdb.Query120, businessID)
		_, _ = testdb.Exec(ctx, db, testdb.Query121, tenantID, otherTenantID)
		_, _ = testdb.Exec(ctx, db, testdb.Query122, tenantID, otherTenantID)
	})

	insertTenant(t, db, ctx, tenantID, "Owner")
	insertTenant(t, db, ctx, otherTenantID, "Other")
	mustExec(t, db, ctx, testdb.Query123, businessID, tenantID)

	competitors := NewCompetitorStore(db)
	created, err := competitors.CreateManual(ctx, CreateManualCompetitorParams{
		TenantID: tenantID, BusinessID: businessID, Name: "Rival Clinic",
		Aliases: []string{"Rival", " Rival "},
	})
	if err != nil {
		t.Fatalf("create manual competitor: %v", err)
	}
	if created.Source != "manual" || created.Status != CompetitorStatusTracked || len(created.Aliases) != 1 {
		t.Fatalf("created = %+v", created)
	}
	if _, err := competitors.CreateManual(ctx, CreateManualCompetitorParams{
		TenantID: otherTenantID, BusinessID: businessID, Name: "Leaked",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant create error = %v, want ErrNotFound", err)
	}

	mustExec(t, db, ctx, testdb.Query124, promptID, businessID)
	mustExec(t, db, ctx, testdb.Query125, runID, businessID)
	mustExec(t, db, ctx, testdb.Query126, resultID, runID, promptID)
	mustExec(t, db, ctx, testdb.Query127, resultID)
	mustExec(t, db, ctx, testdb.Query128, mentionID, resultID, created.ID)

	err = NewAnalysisStore(db).CommitReconcile(ctx, tenantID, businessID, ReconcileCommitParams{
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
		TenantID: tenantID, CompetitorID: created.ID, Alias: "Rival Medical",
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
		TenantID: tenantID, CompetitorID: created.ID, Alias: "Reject Me",
	})
	if err != nil {
		t.Fatalf("reject normalized suggested alias: %v", err)
	}
	if len(rejected.SuggestedAliases) != 0 || len(rejected.Aliases) != 2 {
		t.Fatalf("record after reject = %+v", rejected)
	}
	if _, err := competitors.RejectSuggestedAlias(ctx, SuggestedAliasParams{
		TenantID: tenantID, CompetitorID: created.ID, Alias: "Reject Me",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale reject error = %v, want ErrNotFound", err)
	}
	mustExec(t, db, ctx, testdb.Query129, created.ID)
	if _, err := competitors.ApproveSuggestedAlias(ctx, SuggestedAliasParams{
		TenantID: otherTenantID, CompetitorID: created.ID, Alias: "Private Alias",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant approve error = %v, want ErrNotFound", err)
	}
	aliasesUpdated, err := competitors.UpdateAliases(ctx, UpdateCompetitorAliasesParams{
		TenantID: tenantID, CompetitorID: created.ID,
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
		TenantID: otherTenantID, CompetitorID: created.ID, Aliases: []string{},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant aliases error = %v, want ErrNotFound", err)
	}

	if _, err := competitors.SetStatus(ctx, SetCompetitorStatusParams{
		TenantID: tenantID, CompetitorID: created.ID, Status: CompetitorStatusDismissed,
	}); err != nil {
		t.Fatalf("dismiss competitor: %v", err)
	}
	restored, err := competitors.SetStatus(ctx, SetCompetitorStatusParams{
		TenantID: tenantID, CompetitorID: created.ID, Status: CompetitorStatusTracked,
	})
	if err != nil {
		t.Fatalf("re-track competitor: %v", err)
	}
	if restored.Status != CompetitorStatusTracked {
		t.Fatalf("restored status = %q", restored.Status)
	}
	if _, err := competitors.SetStatus(ctx, SetCompetitorStatusParams{
		TenantID: otherTenantID, CompetitorID: created.ID, Status: CompetitorStatusDismissed,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant status error = %v, want ErrNotFound", err)
	}

	var mentions int
	if err := testdb.QueryRow(ctx, db, testdb.Query130, created.ID).Scan(&mentions); err != nil {
		t.Fatalf("count mentions: %v", err)
	}
	if mentions != 1 {
		t.Fatalf("mentions after dismiss/re-track = %d, want 1", mentions)
	}
}
