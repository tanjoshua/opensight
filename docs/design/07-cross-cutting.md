# Design 07 — Cross-Cutting Concerns

Depends on: all previous designs; closes their open questions.

## Auth and accounts

**Email + password with server-side sessions, hand-rolled in Go. Invite-only signup for MVP.**

- Passwords hashed with argon2id; sessions are random tokens, stored hashed, in a `sessions` table (Postgres), delivered as `HttpOnly, Secure, SameSite=Lax` cookies. Logout = delete row. No JWTs — nothing to revoke-by-expiry when sessions are just rows.
- Rationale: a managed provider (Clerk/Auth0) adds an external dependency and an eventual cost floor for what is, at invite-only scale, ~200 lines of well-trodden Go. Self-hosted identity servers (Keycloak/Ory) are overkill on a 4GB VPS. Revisit only when self-serve signup + password reset + email verification become real needs — that is the point where a managed provider starts paying for itself.
- **Invite-only**: accounts are created by an admin CLI command (`opensight user create --tenant …`), matching founder-led sales for SG clinics. The PRD's self-serve success criteria all happen *after* login (onboarding flow, 03), so this doesn't compromise them. Self-serve signup + billing arrive together post-MVP.
- CSRF: state-changing endpoints require a custom header (`X-Requested-With`), which cross-origin forms cannot set; combined with SameSite=Lax this is sufficient for a JSON-only API.
- API rate limiting: Caddy-level per-IP limit on `/api/`; nothing fancier until abuse exists.

## Secrets and config

- Twelve-factor env vars, loaded from an `.env` file on the VPS (mode 600, outside the repo) referenced by docker-compose. No secret manager at this scale.
- Inventory: Postgres passwords, OpenAI API key, healthcheck ping URLs. The OpenAI key is a **project-scoped key** with a monthly budget cap set in the OpenAI dashboard — the hard backstop (see spend guardrails).
- App config (model ids, extraction version, concurrency caps) also env-driven, with defaults in code; no config service.

## Database migrations

`goose` migrations embedded in the binary, run explicitly via `opensight migrate` during deploy (not on startup — a bad migration shouldn't crash-loop the API). Both the app schema and the seeded `starter` plan row live in migrations.

## Local development

- `make up`: Postgres + Temporal (+ UI) run in Docker, migrations run once, and the Go API/worker run natively with `air`; the script waits for `/healthz`, prints service links, then reports the API healthy. When the frontend is present, Vite also runs natively on a strict local port and proxies `/api`.
- `docker compose -f compose.dev.yml up`: still available for infrastructure-only debugging.
- **`PromptRunner` stub mode** (env-selected): development and tests must not spend OpenAI money or wait on real searches. Two flavors: `stub` (canned, deterministic fixtures — a fake clinic-recommendation response with citations) and `replay` (recorded real `raw_response` payloads checked into `testdata/`). The analysis pipeline (05) develops almost entirely against replay data — real responses, zero cost, deterministic tests.
- Seed command: `opensight seed dev` creates only a tenant and login account. The developer completes the normal onboarding flow to create the business profile and initial prompts, keeping the end-to-end onboarding path exercised during local development.

## Deployment

- Git repo (private, GitHub).
- CI (GitHub Actions): test + lint + build a single multi-stage Docker image (Go binary with embedded SPA) pushed to GHCR.
- Deploy = SSH script: `docker compose pull && docker compose up -d` on the VPS, then `opensight migrate`. No orchestrator, no blue/green; seconds of downtime at deploy is acceptable for this product. Compose stack per 01-D8: `app`, `worker`, `postgres`, `temporal`, `temporal-ui` (bound to localhost only, reached via SSH tunnel), `caddy` (auto-HTTPS).

## Backups and recovery

- Nightly `pg_dump` of `opensight` + `temporal` databases → **restic** encrypted repository → offsite (Hetzner Storage Box or Backblaze B2; both are ~single-digit €/month at this volume — within budget).
- The `.env` file is included in the restic set (it's the only non-reproducible thing outside Postgres).
- **Restore drill is part of MVP acceptance**: on a scratch VPS, restore last night's dump, `docker compose up`, confirm the app serves and schedules resume. An untested backup is a hope, not a backup. Recovery point of ≤24h is fine — worst case a week's run re-executes (idempotency keys make that safe, 04).

## Observability and spend guardrails

Kept deliberately minimal for MVP:

- **Errors**: Sentry free tier for Go + React (or self-hosted GlitchTip later if cost/data-locality demands; free tier is within budget policy).
- **Logs**: structured `slog` JSON to stdout → `docker logs` with rotation. No Loki/ELK; grep is fine at this scale.
- **Workflow debugging**: Temporal UI (that's what it's in the stack for). Stuck or silently failing weekly runs surface here and in `monitoring_runs.status` — checked manually; no external uptime/dead-man's-switch service in MVP.
- **Spend guardrail**: the OpenAI dashboard monthly budget cap on the project-scoped key is the hard backstop — no in-app circuit breaker. The per-tenant cost query (04) still exists for unit economics, run ad hoc.

## Data protection (light-touch, noted not lawyered)

The authenticated Privacy page is PDPA-aware plain-language product copy, not a compliance certification or substitute for legal terms. It accurately enumerates what the product stores: account email, password verifier and session records; business profile and public practitioner data; prompts and lineage; raw monitoring responses, citations and request metadata; derived analysis and metrics; and plan, schedule, run, usage and cost configuration.

**Hard usage boundary: users must never enter patient-identifiable data.** Prompts are generic consumer queries rather than patient cases; generation rules and review copy reinforce this boundary. The product does not claim automatic detection or prevention of every prohibited entry.

Monitoring prompts and relevant business context are sent to the OpenAI API and may use web search. Public copy qualifies that OpenAI handling depends on the applicable service agreement and deployed account settings; it does not invent promises about external retention or training.

The service is pre-production. Singapore hosting is planned before production, but the product must not claim a production region until FND-5 deploys and verifies it. Monitoring results are retained indefinitely to preserve trends and evidence unless later terms or policy specify otherwise; legal, security, or dispute-preservation needs can override ordinary deletion. Account and profile data is retained as needed to operate the service and customer relationship, with specific deletion or post-account terms confirmed through the customer's account representative rather than invented here.

## Open items deliberately left post-MVP

Self-serve signup, password reset emails (invite-only sidesteps both), billing (Stripe, per 01's billing-ready entitlements), competitor merge (05), metrics/Prometheus, multi-VPS.
