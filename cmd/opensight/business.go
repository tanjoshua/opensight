package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"opensight/internal/config"
	"opensight/internal/domain"
	"opensight/internal/llm"
	"opensight/internal/store"
	"opensight/internal/workflows"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"gopkg.in/yaml.v3"
)

// businessCreateOptions carries the parsed inputs for `business create`.
type businessCreateOptions struct {
	TenantID domain.ID
	Spec     businessSpec
}

// businessSpec is a business profile plus its prompts, ready for the store: the
// jsonb columns are already marshalled to raw JSON.
type businessSpec struct {
	Name          string
	Website       *string
	Category      *string
	Aliases       []string
	Practitioners json.RawMessage
	Services      json.RawMessage
	Location      json.RawMessage
	Prompts       []string
}

// businessSpecFile is the on-disk YAML/JSON shape (JSON is valid YAML, so one
// decoder covers both). location/practitioners/services are held as generic
// values and re-marshalled to jsonb.
type businessSpecFile struct {
	Name          string           `yaml:"name"`
	Website       string           `yaml:"website"`
	Category      string           `yaml:"category"`
	Aliases       []string         `yaml:"aliases"`
	Location      map[string]any   `yaml:"location"`
	Practitioners []map[string]any `yaml:"practitioners"`
	Services      []map[string]any `yaml:"services"`
	Prompts       []string         `yaml:"prompts"`
}

func parseBusinessCreateArgs(args []string) (businessCreateOptions, error) {
	flags := flag.NewFlagSet("business create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tenantRaw := flags.String("tenant", "", "tenant id")
	file := flags.String("file", "", "path to the business spec (YAML or JSON)")
	if err := flags.Parse(args); err != nil {
		return businessCreateOptions{}, fmt.Errorf("%s", businessCreateUsage)
	}
	if flags.NArg() != 0 {
		return businessCreateOptions{}, fmt.Errorf("unexpected argument %q; %s", flags.Arg(0), businessCreateUsage)
	}
	if strings.TrimSpace(*tenantRaw) == "" {
		return businessCreateOptions{}, fmt.Errorf("--tenant is required; %s", businessCreateUsage)
	}
	if strings.TrimSpace(*file) == "" {
		return businessCreateOptions{}, fmt.Errorf("--file is required; %s", businessCreateUsage)
	}

	tenantID, err := uuid.Parse(strings.TrimSpace(*tenantRaw))
	if err != nil {
		return businessCreateOptions{}, fmt.Errorf("--tenant must be a UUID: %w", err)
	}

	spec, err := loadBusinessSpec(*file)
	if err != nil {
		return businessCreateOptions{}, err
	}
	return businessCreateOptions{TenantID: tenantID, Spec: spec}, nil
}

// loadBusinessSpec reads and validates a business spec file into a store-ready
// businessSpec.
func loadBusinessSpec(path string) (businessSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return businessSpec{}, fmt.Errorf("read spec file: %w", err)
	}

	var file businessSpecFile
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		return businessSpec{}, fmt.Errorf("parse spec file: %w", err)
	}

	spec := businessSpec{
		Name:    strings.TrimSpace(file.Name),
		Aliases: file.Aliases,
		Prompts: trimPrompts(file.Prompts),
	}
	if spec.Name == "" {
		return businessSpec{}, errors.New("spec: name is required")
	}
	if len(spec.Prompts) == 0 {
		return businessSpec{}, errors.New("spec: at least one prompt is required")
	}
	if website := strings.TrimSpace(file.Website); website != "" {
		spec.Website = &website
	}
	if category := strings.TrimSpace(file.Category); category != "" {
		spec.Category = &category
	}

	if spec.Location, err = marshalJSONField("location", file.Location); err != nil {
		return businessSpec{}, err
	}
	if spec.Practitioners, err = marshalJSONSlice("practitioners", file.Practitioners); err != nil {
		return businessSpec{}, err
	}
	if spec.Services, err = marshalJSONSlice("services", file.Services); err != nil {
		return businessSpec{}, err
	}

	// Location is load-bearing for web-search visibility (design 04); reject a
	// bad or missing one at parse time rather than after writing rows.
	if _, err := llm.LocationFromBusinessJSON(spec.Location); err != nil {
		return businessSpec{}, fmt.Errorf("spec location: %w", err)
	}
	return spec, nil
}

func trimPrompts(prompts []string) []string {
	trimmed := make([]string, 0, len(prompts))
	for _, p := range prompts {
		if p = strings.TrimSpace(p); p != "" {
			trimmed = append(trimmed, p)
		}
	}
	return trimmed
}

func marshalJSONField(name string, value map[string]any) (json.RawMessage, error) {
	if len(value) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("spec %s: %w", name, err)
	}
	return raw, nil
}

func marshalJSONSlice(name string, value []map[string]any) (json.RawMessage, error) {
	if len(value) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("spec %s: %w", name, err)
	}
	return raw, nil
}

// createBusinessCLI seeds an active business (profile + prompts) from a spec
// file, creates its recurring monitoring Schedule, and triggers the first run
// with trigger=initial (RUN-5). Onboarding UI is Phase 3; this is how internal
// test businesses go live meanwhile (design README, Phase 1).
func createBusinessCLI(ctx context.Context, cfg config.Config, opts businessCreateOptions, out io.Writer) error {
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

	// Fail fast on plan lookup: the schedule spec derives from run_interval, and a
	// missing tenant/plan should stop us before we write a business.
	plan, err := admin.GetTenantPlan(ctx, opts.TenantID)
	if err != nil {
		return fmt.Errorf("load tenant plan: %w", err)
	}

	activatedAt := time.Now().UTC()
	business, err := businesses.CreateBusiness(ctx, store.CreateBusinessParams{
		TenantID:      opts.TenantID,
		Status:        store.BusinessStatusActive,
		Name:          opts.Spec.Name,
		Website:       opts.Spec.Website,
		Aliases:       opts.Spec.Aliases,
		Category:      opts.Spec.Category,
		Practitioners: opts.Spec.Practitioners,
		Services:      opts.Spec.Services,
		Location:      opts.Spec.Location,
		ActivatedAt:   &activatedAt,
	})
	if err != nil {
		return fmt.Errorf("create business: %w", err)
	}

	for _, text := range opts.Spec.Prompts {
		if _, err := prompts.CreateActivePrompt(ctx, store.CreateActivePromptParams{
			TenantID:   opts.TenantID,
			BusinessID: business.ID,
			Text:       text,
		}); err != nil {
			return fmt.Errorf("create prompt %q: %w", text, err)
		}
	}

	temporalClient, err := dialTemporal(ctx, cfg)
	if err != nil {
		return err
	}
	defer temporalClient.Close()

	scheduleID, err := workflows.CreateMonitorSchedule(ctx, temporalClient, workflows.CreateScheduleParams{
		BusinessID:  business.ID,
		Platform:    store.PlatformChatGPT,
		RunInterval: plan.RunInterval,
		TaskQueue:   cfg.TemporalTaskQueue,
	})
	if err != nil {
		return err
	}

	scheduledFor := truncateToDay(activatedAt)
	workflowID := workflows.RunWorkflowID(business.ID, store.PlatformChatGPT, scheduledFor)
	if _, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: cfg.TemporalTaskQueue,
	}, workflows.RunWorkflow, workflows.RunWorkflowInput{
		BusinessID:   business.ID,
		Platform:     store.PlatformChatGPT,
		ScheduledFor: scheduledFor,
		Trigger:      store.RunTriggerInitial,
	}); err != nil {
		return fmt.Errorf("start first run: %w", err)
	}

	_, err = fmt.Fprintf(out,
		"created business\ntenant_id=%s\nbusiness_id=%s\nprompts=%d\nrun_interval=%s\nschedule_id=%s\nfirst_run_workflow_id=%s\n",
		opts.TenantID, business.ID, len(opts.Spec.Prompts), plan.RunInterval, scheduleID, workflowID,
	)
	return err
}
