# Design 04 — Monitoring Jobs

Depends on: [01 Architecture](01-architecture.md), [02 Data Model](02-data-model.md), [08 Billing](08-billing.md)

## Weekly scheduling

River inserts one scheduler-sweep job every 15 minutes and once on app startup. The sweep loads active businesses, resolves plan entitlements and current billing access, and ignores anything without full access.

Starter businesses run weekly at 02:00 UTC on a stable weekday derived from the business UUID. For each eligible business the sweep calculates the latest due slot. If the app was down, only that latest slot is inserted; historical slots are never backfilled. A slot before `activated_at` is not due. `next_run_at` uses the same deterministic calculation from PostgreSQL state.

Monitoring args are unique by business, platform, and scheduled slot. The database's `UNIQUE (business_id, platform, scheduled_for)` is the final idempotency anchor.

## Monitoring job

The job recomputes billing access before any spend or run write. It snapshots active prompts and location, upserts a running `monitoring_runs` row with the River `job_id`, and runs prompts concurrently under the process-wide LLM limiter.

Each prompt makes up to four context-aware attempts. Provider refusals and non-retryable request errors are recorded immediately; exhausted transient failures are also recorded as failed results. Successful siblings are never discarded. Final status is recomputed from the stored prompt snapshot (`completed`, `partial`, or `failed`), then an analysis job is inserted.

## Recovery and operations

River may retry a coarse job after a crash. Existing `(run_id, prompt_id)` result rows prevent repeated model calls, finalization is a recomputation, and downstream job uniqueness suppresses duplicate live work.

Structured logs carry job/run/result identifiers. Queue state is inspected without another service:

```sql
SELECT id, kind, queue, state, attempt, max_attempts, scheduled_at, attempted_at, errors
FROM river_job
WHERE state NOT IN ('completed', 'cancelled', 'discarded')
ORDER BY scheduled_at, id;
```

User-visible progress comes only from `monitoring_runs`, `prompt_results`, and `result_analyses`; River internals are never exposed as product state.
