# Epic 04 — Run Pipeline (RUN)

PromptRunner, the weekly RunWorkflow, schedules, and dev/test execution modes. Phase 1.

---

## RUN-1 — PromptRunner interface + OpenAI implementation

As the developer, I want prompt execution behind a `PromptRunner` interface with an OpenAI Responses implementation, so that platforms are additive and every result is reproducible.

- [x] `internal/llm` defines `PromptRunner`; OpenAI impl uses Responses API with `web_search` tool and **`store: false`**.
- [x] `user_location` built from the business profile's `location` (country required, city/area if present) — nothing hardcodes Singapore.
- [x] No system prompt beyond the user's prompt text.
- [x] Exact request params (model, user_location, tool config) returned for persistence to `prompt_results.request`; the **response's reported model id** is what gets stored, never the config value.
- [ ] OpenAI key is a project-scoped key; monthly budget cap set in the OpenAI dashboard (the hard spend backstop).

Deps: SCH-3 · Phase 1 · Ref: design 01 (D1), 04 (ExecutePrompt), 07 (Secrets, Spend guardrail)

## RUN-2 — Stub/replay modes and dev seed

As the developer, I want env-selected `stub` and `replay` PromptRunner modes plus `opensight seed dev`, so that development and tests never spend OpenAI money.

- [x] `stub`: canned deterministic fixture (fake clinic-recommendation response with citation annotations).
- [x] `replay`: recorded real `raw_response` payloads from `testdata/`.
- [x] `opensight seed dev`: tenant, user, active business, prompts, and two runs of replay results — every UI section has data on first boot.
- [x] Replay fixtures start from the SPK-1 spike captures.
- [ ] Refreshed with at least one response recorded through RUN-1 once it works (blocked on RUN-1's project-scoped key/budget cap — no live call has been made yet).

Deps: RUN-1, SPK-1 · Phase 1 · Ref: design 07 (Local development)

## RUN-3 — RunWorkflow skeleton

As the operator, I want a Temporal `RunWorkflow` that upserts the run, snapshots prompts, and finalizes status, so that runs are idempotent and partial failure is a first-class state.

- [x] Workflow id `run-{business_id}-chatgpt-{scheduled_for}`; duplicate triggers converge (DB upsert via `LoadRunSpec` returns the existing run for that date — no-op).
- [x] `LoadRunSpec` records `trigger` (`initial|scheduled|manual`), sets status `running`, snapshots active prompts at start (mid-run prompt replacement cannot produce a half-and-half run). Plan entitlements need no separate snapshot: `PromptStore.CreateActivePrompt` enforces `plan.prompt_limit` at write time, so the active-prompt list is already entitlement-bounded by construction.
- [x] `FinalizeRun`: all succeeded → `completed`; some → `partial`; none → `failed`.
- [x] Worker registered and running in `work` mode locally.
- [ ] Worker running in prod compose — blocked on FND-5 (prod `compose.yml` does not exist yet).

Deps: SCH-3, FND-2 · Phase 1 · Ref: design 04 (RunWorkflow)

## RUN-4 — ExecutePrompt activity

As the operator, I want per-prompt execution with correct retry semantics, so that transient failures retry and refusals don't.

- [x] Fan-out from RunWorkflow, max ~4 concurrent (worker activity-slot cap via `MaxConcurrentActivityExecutionSize`, set from `PROMPT_CONCURRENCY`).
- [x] Idempotent: returns the existing `prompt_results` row for `(run_id, prompt_id)` if present; the UNIQUE constraint makes races error, never duplicate (re-get on `ErrDuplicateResult`).
- [x] Persists `request`, `raw_response`, `response_text`, reported `model`, timestamps; failures record `error`.
- [x] Retry: 4 attempts, exponential backoff from 10s, 120s per-attempt timeout; 400-class errors and content-policy refusals are **non-retryable** (a refusal is a finding, recorded as a failed result).

Deps: RUN-1, RUN-3 · Phase 1 · Ref: design 04 (ExecutePrompt)

## RUN-5 — Schedules and business-seeding CLI

As the operator, I want a weekly Temporal Schedule per active business and a CLI to seed a business, so that internal test businesses run weekly without onboarding UI (that's Phase 3).

- [ ] Schedule id `monitor-{business_id}-chatgpt`; spec derived from `plan.run_interval`; overlap policy **Skip**; per-business jitter (hash → day-of-week offset).
- [ ] `opensight business create` (or seed subcommand): creates an active business with profile + prompts from a YAML/JSON file, creates the schedule, and triggers the first run (`trigger=initial`).
- [ ] Deactivation path documented (pause schedule; data stays).

Deps: RUN-3, AUTH-2 · Phase 1 · Ref: design 04 (Scheduling), design README (Phase 1: CLI-seeded profile)

## RUN-6 — Cost query

As the operator, I want a per-tenant cost query over stored token usage, so that week-one reality checks the cost ballpark ($0.50–2/tenant/week).

- [ ] Ad-hoc SQL (checked into `docs/` or a CLI subcommand) summing token usage from `raw_response` per tenant per week.
- [ ] Run against the first real production run; result recorded against the ballpark.

Deps: RUN-4 · Phase 1 · Ref: design 04 (Cost model), 07 (Observability)
