# Design 04 — Monitoring Pipeline

Depends on: [01 Architecture](01-architecture.md), [02 Data Model](02-data-model.md) (`monitoring_runs`, `prompt_results`), [03 Onboarding](03-onboarding.md) (schedule creation, immediate first run)

## Scheduling

One Temporal Schedule per active business: id `monitor-{business_id}-chatgpt`, spec derived from `plan.run_interval` (weekly for Starter). Per-business jitter (hash of business id → day-of-week offset within the week) spreads load and avoids every account's data updating in the same hour — irrelevant for capacity at MVP scale, but it makes API rate-limit spikes and cost spikes smoother from day one.

- Overlap policy: **Skip** — if a run is somehow still in flight when the next fires, skip rather than stack.
- Business deactivation (churn, future) = pause/delete the schedule; data stays.
- Prompt set changes take effect on the next run: the workflow snapshots active prompts at start, so a mid-run replacement can't produce a half-and-half run.

## RunWorkflow

Workflow id: `run-{business_id}-chatgpt-{scheduled_for}` — deterministic, so schedule fires, the onboarding "first run now" (03), and any future manual "run now" all converge on the same idempotency behavior. The DB uniqueness `(business_id, platform, scheduled_for)` is the second layer: `LoadRunSpec` does an upsert and returns the existing run if one exists for that date, making duplicate triggers no-ops. First-run-on-Wednesday vs schedule-on-Sunday coexist naturally: different `scheduled_for` dates, two runs that week, harmless. `LoadRunSpec` records the run's `trigger` (`initial` | `scheduled` | `manual`); trends plot per-run ordered by `scheduled_for` regardless of trigger.

```
RunWorkflow(businessID, platform, scheduledFor)
 ├─ CheckRunAccess     activity: resolve account, derive billing.Access (08) fresh
 │                     against now; anything but full returns the workflow
 │                     immediately as a skip — no run row, no prompt, no
 │                     analysis. This is design 08's authoritative spend
 │                     backstop (gate 3): it recomputes on every run start, so
 │                     a delayed or dropped Stripe webhook can never turn into
 │                     spend, unlike the RPC gate (08 gate 1) and the schedule
 │                     pause (08 gate 2), which both depend on one arriving.
 │                     Access is read once, here — a run already in flight
 │                     when access drops is allowed to finish.
 ├─ LoadRunSpec        activity: resolve account via store.ResolveAccountID,
 │                     then upsert monitoring_runs(status=running,
 │                     expected_results=len(prompts)); snapshot active prompts
 │                     (already entitlement-bounded: prompt_limit is enforced
 │                     at prompt-write time)
 ├─ ExecutePrompt ×N   activities, fan-out, max ~4 concurrent
 ├─ FinalizeRun        activity: set status completed | partial | failed,
 │                     reading expected_results back off the row (not a
 │                     workflow-passed argument) so a later prompt
 │                     replacement can't change a historical run's target.
 │                     Runs before AnalyzeRun (monitoring_runs' CHECK forbids
 │                     analysis_completed_at unless completed_at is already
 │                     set) — so a healthy run sits terminal-but-unanalyzed
 │                     for the whole span below, which the UI (06) renders as
 │                     an in-progress "Analyzing" state, not an error
 └─ AnalyzeRun         child workflow (design 05) — independent retry budget,
                        a failed analysis never re-spends prompt executions
```

Status rules: all prompts succeeded → `completed`; some → `partial`; none → `failed`. Analysis failure does not change run status (results exist and are viewable raw); it flags the run for re-analysis instead.

## ExecutePrompt

One activity per prompt. Idempotent: first thing it does is check for an existing `prompt_results` row for `(run_id, prompt_id)` and return it if present (covers activity retries after a success whose ack was lost) — backed by the `UNIQUE (run_id, prompt_id)` constraint (02), so a race can only error, never duplicate.

Request (via the `PromptRunner` interface):

- OpenAI Responses API via the official Go SDK, `web_search` tool enabled, **`store: false`** — OpenAI retains nothing server-side; our `raw_response` is the system of record.
- The exact request parameters (model, `user_location`, tool config) are persisted to `prompt_results.request`, so every stored result is reproducible and interpretable later.
- **`user_location` set from the business profile's `location` (country, city, area — see 02).** This is load-bearing for a local-visibility product: search-grounded answers vary by inferred location, and we want the answer a user *near the business* would get, not a US datacenter's. Nothing hardcodes Singapore; new markets and future multi-location support (PRD §9) are profile data, not code changes.
- Model: configured default (the closest available proxy for consumer ChatGPT's default tier); the response's reported model id is what gets stored — never the config value.
- No system prompt beyond the user's prompt text — we are simulating a cold consumer query, not engineering a better answer.
- Timeout 120s per attempt.

Retry policy: 4 Temporal activity attempts, exponential backoff starting 10s. SDK automatic retries are disabled so each activity attempt makes exactly one provider call. Non-retryable: 400-class request errors and content-policy refusals — those fail the result immediately with `error` recorded (a refusal is a *finding*, not an outage). 429/5xx retry with backoff; the global concurrency cap (~4, worker-level activity slot limit) is the primary rate-limit courtesy.

## Cost model

Per prompt: one Responses call with web search — tokens plus per-search-call fees; ballpark **single-digit cents per prompt, so roughly $0.50–2 per account per week**. Two requirements fall out:

1. **Measure, don't assume**: the Responses payload includes token usage; it lives inside `raw_response`, so per-account cost reporting is a query, no schema change. Build that query early and check the ballpark against reality in week one.
2. Cost scales linearly with accounts and with `prompt_limit` — pricing of future tiers (PRD §9) must account for it, which is another reason limits live in the `internal/billing` catalog (08), not a literal.

## Operations

- Temporal UI (already in the compose stack) is the ops surface: stuck runs, retry states, failure causes.
- The app-facing view of health is `monitoring_runs.status` plus per-result `status`/`error` — PRD §5's "run status", shown in the Runs section (06).
- A `partial` run that stays partial after retries is acceptable and visible; there is no automatic re-run of individual failed prompts in MVP (manual re-trigger via Temporal UI if it ever matters).
- **Run progress.** User-facing progress (the four-stage "Preparing / Asking ChatGPT / Analyzing / Done" strip, 06) is derived entirely from `monitoring_runs`/`prompt_results`/`result_analyses` counts on the existing poll — never from Temporal workflow history. Temporal history and the Temporal Web UI stay ops-only; there is no user-facing link into it.

## Open questions (owned by later increments)

- **05**: AnalyzeRun internals — extraction calls, alias matching, competitor discovery writes, re-analysis flow.
- **06**: how run status and partial results render in the Runs section.
- **07**: OpenAI key management, spend alerting (a runaway loop is the main cost risk on a self-hosted stack with no cloud billing alarms).
