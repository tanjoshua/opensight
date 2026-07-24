package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"opensight/internal/auth"
	"opensight/internal/config"
	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"
	"opensight/internal/workflows"

	"github.com/google/uuid"
)

// Fixed UUIDv7-form ids make `opensight seed dev` idempotent: a second run
// detects the seed business and no-ops instead of creating duplicates.
var (
	seedTenantID   = uuid.MustParse("01950000-0000-7000-8000-0000000000d0")
	seedUserID     = uuid.MustParse("01950000-0000-7000-8000-0000000000d1")
	seedBusinessID = uuid.MustParse("01950000-0000-7000-8000-0000000000d2")
	seedPromptID1  = uuid.MustParse("01950000-0000-7000-8000-0000000000e1")
	seedPromptID2  = uuid.MustParse("01950000-0000-7000-8000-0000000000e2")
	seedRunID1     = uuid.MustParse("01950000-0000-7000-8000-0000000000f1")
	seedRunID2     = uuid.MustParse("01950000-0000-7000-8000-0000000000f2")
)

const (
	seedTenantName   = "Roots Dev Tenant"
	seedBusinessName = "Roots! Advanced Endodontics"
	seedEmail        = "dev@opensight.local"
	seedPassword     = "opensight-dev"
	seedPlatform     = "chatgpt"
	seedLocationJSON = `{"country":"SG","city":"Singapore","region":"Central","timezone":"Asia/Singapore"}`
)

// seedDevCLI creates a fully populated tenant — user, active business, prompts,
// and two runs of replay results — so every section of the UI has data on first
// boot (design 07 "Local development"). It uses replay mode, so it spends no
// OpenAI money.
func seedDevCLI(ctx context.Context, cfg config.Config, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}

	db, err := store.Open(cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	admin := store.NewAdminStore(db)
	businesses := store.NewBusinessStore(db)
	prompts := store.NewPromptStore(db)
	runs := store.NewRunStore(db)
	results := store.NewResultStore(db)

	runner, err := llm.NewReplayPromptRunner()
	if err != nil {
		return err
	}
	location, err := llm.LocationFromBusinessJSON(json.RawMessage(seedLocationJSON))
	if err != nil {
		return err
	}

	if _, err := businesses.ResolveTenantID(ctx, seedBusinessID); err == nil {
		_, err = fmt.Fprintln(out, "dev data already seeded; nothing to do")
		return err
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	tenant, err := admin.CreateTenant(ctx, store.CreateTenantParams{ID: seedTenantID, Name: seedTenantName})
	if err != nil {
		return err
	}

	passwordHash, err := auth.HashPassword(seedPassword)
	if err != nil {
		return err
	}
	if _, err := admin.CreateUser(ctx, store.CreateUserParams{
		ID:           seedUserID,
		TenantID:     tenant.ID,
		Email:        seedEmail,
		PasswordHash: passwordHash,
	}); err != nil {
		return err
	}

	activatedAt := time.Now().UTC()
	website := "https://example.com/roots-endodontics"
	category := "Dental"
	if _, err := businesses.CreateBusiness(ctx, store.CreateBusinessParams{
		ID:          seedBusinessID,
		TenantID:    tenant.ID,
		Status:      store.BusinessStatusActive,
		Name:        seedBusinessName,
		Website:     &website,
		Category:    &category,
		Location:    json.RawMessage(seedLocationJSON),
		ActivatedAt: &activatedAt,
	}); err != nil {
		return err
	}

	promptDefs := []struct {
		id   domain.ID
		text string
	}{
		{seedPromptID1, "What are the best dental clinics in Singapore?"},
		{seedPromptID2, "Is there a good dental clinic near Tampines?"},
	}
	for _, def := range promptDefs {
		if _, err := prompts.CreateActivePrompt(ctx, store.CreateActivePromptParams{
			ID:         def.id,
			TenantID:   tenant.ID,
			BusinessID: seedBusinessID,
			Text:       def.text,
		}); err != nil {
			return err
		}
	}

	// Two runs of history. The first fully succeeds; the second fails one prompt
	// so the UI's partial-run and failed-result states also have data.
	runDefs := []struct {
		id           domain.ID
		scheduledFor time.Time
		trigger      store.RunTrigger
		failPromptID domain.ID
	}{
		{seedRunID1, workflows.TruncateToDay(activatedAt.AddDate(0, 0, -7)), store.RunTriggerInitial, uuid.Nil},
		{seedRunID2, workflows.TruncateToDay(activatedAt), store.RunTriggerScheduled, seedPromptID2},
	}

	resultCount := 0
	for _, rd := range runDefs {
		workflowID := fmt.Sprintf("run-%s-%s-%s", seedBusinessID, seedPlatform, rd.scheduledFor.Format("2006-01-02"))
		run, err := runs.UpsertRun(ctx, tenant.ID, store.UpsertRunParams{
			ID:           rd.id,
			BusinessID:   seedBusinessID,
			Platform:     seedPlatform,
			Trigger:      rd.trigger,
			ScheduledFor: rd.scheduledFor,
			WorkflowID:   workflowID,
		})
		if err != nil {
			return err
		}

		for _, def := range promptDefs {
			res, err := runner.RunPrompt(ctx, llm.PromptRequest{Prompt: def.text, Location: location})
			if err != nil {
				return fmt.Errorf("replay prompt %q: %w", def.text, err)
			}
			params := store.CreateResultParams{
				RunID:       rd.id,
				PromptID:    def.id,
				Request:     res.RequestJSON,
				RequestedAt: rd.scheduledFor,
				CompletedAt: rd.scheduledFor.Add(time.Minute),
			}
			if def.id == rd.failPromptID {
				errText := "openai: status 429: rate limited (seeded failure example)"
				params.Status = store.ResultStatusFailed
				params.Error = &errText
			} else {
				model := res.Model
				text := res.ResponseText
				params.Status = store.ResultStatusSucceeded
				params.Model = &model
				params.RawResponse = res.RawResponse
				params.ResponseText = &text
			}
			if _, err := results.CreateResult(ctx, tenant.ID, params); err != nil {
				return err
			}
			resultCount++
		}

		if _, err := runs.FinalizeRun(ctx, tenant.ID, run.ID, len(promptDefs)); err != nil {
			return err
		}
	}

	_, err = fmt.Fprintf(out,
		"seeded dev data\ntenant_id=%s\nuser_email=%s\npassword=%s\nbusiness_id=%s\nprompts=%d\nruns=%d\nresults=%d\n",
		tenant.ID, seedEmail, seedPassword, seedBusinessID, len(promptDefs), len(runDefs), resultCount,
	)
	return err
}
