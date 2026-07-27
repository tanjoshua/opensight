# Epic 02 — Core Schema & Store (SCH)

The Phase-1 data model (analysis tables land in epic 07) and the tenant-scoped repository layer. Phase 1.

---

## SCH-1 — Plans and tenancy tables

As the developer, I want `plans`, `tenants`, and `users` migrated with a seeded starter plan, so that entitlements come from data, never constants.

- [x] Migration creates `plans (id, slug UNIQUE, prompt_limit, run_interval, platforms)`, `tenants (id, name, plan_id, created_at)`, `users (id, tenant_id, email citext UNIQUE, created_at)`; all IDs UUIDv7.
- [x] `starter` plan row (20 prompts, weekly, `{chatgpt}`) seeded **in a migration**.
- [x] No code path reads a hardcoded "20" or "weekly" — grep-verifiable.

Deps: FND-3 · Phase 1 · Ref: design 02 (Tenancy and subscription) — the `plans` table this story created is superseded by the entitlement catalog in design 08 (BILL-1)

## SCH-2 — Business, proposal, and prompt tables

As the developer, I want `businesses`, `profile_proposals`, and `prompts` migrated, so that profiles and the prompt-replacement lineage exist in the schema.

- [x] `businesses` per design 02: status `draft|active`, `aliases text[]` (org trading names only), `category`, `services jsonb`, `location jsonb` with **country required** (app-validated), `activated_at`.
- [x] `profile_proposals (payload jsonb, status pending|applied|discarded, resolved_at)`.
- [x] `prompts`: `text` immutable after insert (no update path in the store layer), `status active|retired`, `replaces_prompt_id` FK, `retired_at`.
- [x] App-enforced invariant: `count(active prompts) <= plan.prompt_limit`.

Deps: SCH-1 · Phase 1 · Ref: design 02 (Businesses and profile, Prompts)

## SCH-3 — Runs and results tables

As the developer, I want the append-only `monitoring_runs` and `prompt_results` tables, so that the workflow has its idempotency anchors.

- [x] `monitoring_runs` per design 02 incl. `trigger (initial|scheduled|manual)`, `scheduled_for date`, `status`, `workflow_id`, `analysis_completed_at NULL`, and **`UNIQUE (business_id, platform, scheduled_for)`**.
- [x] `prompt_results` per design 02 incl. `request jsonb`, `raw_response jsonb`, `response_text`, `error`, and **`UNIQUE (run_id, prompt_id)`**.
- [x] No UPDATE path for `raw_response`/`response_text` in the store layer (append-only).

Deps: SCH-2 · Phase 1 · Ref: design 02 (Runs and results)

## SCH-4 — Tenant-scoped repository layer

As the developer, I want all data access to go through repositories that enter via a tenant-checked business lookup, so that there is no unscoped query path.

- [x] `internal/store` repositories for the tables above; every business-owned read/write requires tenant context and validates business→tenant ownership.
- [x] Deeper tables (prompts, runs, results) scope through the business join — no direct-by-id access without the tenant check.
- [x] Tests: cross-tenant access attempts return not-found.

Deps: SCH-3 · Phase 1 · Ref: design 01 (D4 multi-tenancy), 06 (API conventions)

## SCH-5 — Fix broken store integration test fixtures

As the developer, I want the opt-in `internal/store` integration tests to actually pass against a real Postgres, so that the tenant-scoping and constraint guarantees they claim to verify are still checked rather than silently rotted.

- [x] `TestRepositoriesEnforceTenantScoping` compares JSONB services as parsed values rather than relying on Postgres's serialized whitespace.
- [x] `TestCompetitorStoreTenantScopingAndHistoryPreservation` gives its `active` business fixture a valid category, location country, and activation timestamp.
- [x] `OPENSIGHT_STORE_TEST_DATABASE_URL=<dsn> go test ./internal/store/...` passes clean against a freshly migrated database (all migrations applied, no skipped/failing tests).

Deps: SCH-2 (active-profile constraint), SCH-4 (tenant-scoping test) · Phase 1 (backfill) · Ref: design 02 (Businesses and profile — active-profile invariant), 01 (D4 multi-tenancy)
