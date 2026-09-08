# Design 04 — Monitoring Jobs

Depends on: [01 Architecture](01-architecture.md), [02 Data Model](02-data-model.md), [08 Billing](08-billing.md)

## Weekly scheduling

River inserts one scheduler-sweep job every 15 minutes and once on app startup. The sweep loads active businesses, resolves plan entitlements and current billing access, and ignores anything without full access. Businesses whose `businesses.monitoring_paused_at` is set are excluded by the candidate query itself, so an admin pause costs no job insert at all.

Starter businesses run weekly at 02:00 UTC on a stable weekday derived from the business UUID. For each eligible business the sweep calculates the latest due slot. If the app was down, only that latest slot is inserted; historical slots are never backfilled. A slot before `activated_at` is not due. `next_run_at` uses the same deterministic calculation from PostgreSQL state.

Monitoring args are unique by business, platform, and scheduled slot. Before inserting a job, the sweep skips any slot already represented in `monitoring_runs`; the database's `UNIQUE (business_id, platform, scheduled_for)` remains the final idempotency anchor. This durable check prevents a completed slot from being re-enqueued after River removes its completed job.

## Monitoring job

The job recomputes billing access — and re-reads the admin pause switch — before any spend or run write. The sweep already skips paused businesses, so this second read only covers the narrow window where a job was enqueued before the pause or is retrying after it; it is what makes "pause" mean "stop now" rather than "stop next week". A paused business's job completes as a no-op: no run row, no prompt, no analysis. It snapshots active prompts and location into the running `monitoring_runs` row alongside the River `job_id`, and runs prompts concurrently under the process-wide LLM limiter. An upsert conflict reads the stored snapshot instead of current business configuration, so every retry of a scheduled slot executes the same prompt IDs, prompt text, and location.

Each prompt makes up to four context-aware attempts. Provider refusals and non-retryable request errors are recorded immediately; exhausted transient failures are also recorded as failed results. Successful siblings are never discarded. Errors outside that deliberately persisted provider outcome — including result reads/writes, limiter acquisition, and cancellation — fail the monitoring job so River retries it. Final status is recomputed from the stored prompt snapshot (`completed`, `partial`, or `failed`) only after every prompt has a durable result, then an analysis job is inserted.

## Recovery and operations

River may retry a coarse job after a crash. The immutable run `spec` restores its original execution input, existing `(run_id, prompt_id)` result rows prevent repeated model calls, finalization is a recomputation, and downstream job uniqueness suppresses duplicate live work.

Structured logs carry job/run/result identifiers. Queue state is inspected without another service:

```sql
SELECT id, kind, queue, state, attempt, max_attempts, scheduled_at, attempted_at, errors
FROM river_job
WHERE state NOT IN ('completed', 'cancelled', 'discarded')
ORDER BY scheduled_at, id;
```

Local development also runs the self-hosted River UI on `127.0.0.1:8082` for interactive job inspection and control. It is bound to loopback as a development convenience only; production exposes no queue dashboard.

User-visible progress comes from `monitoring_runs`, `prompt_results`, and `result_analyses`. Before the worker creates the first run row, `ListRuns.monitoring_pending` exposes only whether live monitoring work exists so the empty history can say that the first run is being prepared and poll until it appears. River job IDs, retries, errors, and queue states remain operational internals.
