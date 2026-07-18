# Design 07 — Cross-Cutting Concerns

Depends on: all previous designs; closes their open questions.

## Auth and accounts

**Email + password with server-side sessions, hand-rolled in Go. Invite-only signup for MVP.**

- Passwords hashed with argon2id; sessions are random tokens in a `sessions` table (Postgres), delivered as `HttpOnly, Secure, SameSite=Lax` cookies. Logout = delete row. No JWTs — nothing to revoke-by-expiry when sessions are just rows.
- Rationale: a managed provider (Clerk/Auth0) adds an external dependency and an eventual cost floor for what is, at invite-only scale, ~200 lines of well-trodden Go. Self-hosted identity servers (Keycloak/Ory) are overkill on a 4GB VPS. Revisit only when self-serve signup + password reset + email verification become real needs — that is the point where a managed provider starts paying for itself.
- **Invite-only**: accounts are created by an admin CLI command (`opensight user create --tenant …`), matching founder-led sales for SG clinics. The PRD's self-serve success criteria all happen *after* login (onboarding flow, 03), so this doesn't compromise them. Self-serve signup + billing arrive together post-MVP.
- CSRF: state-changing endpoints require a custom header (`X-Requested-With`), which cross-origin forms cannot set; combined with SameSite=Lax this is sufficient for a JSON-only API.
- API rate limiting: Caddy-level per-IP limit on `/api/`; nothing fancier until abuse exists.

## Secrets and config

- Twelve-factor env vars, loaded from an `.env` file on the VPS (mode 600, outside the repo) referenced by docker-compose. No secret manager at this scale.
- Inventory: Postgres passwords, session-cookie signing key, OpenAI API key, healthcheck ping URLs. The OpenAI key is a **project-scoped key** with a monthly budget cap set in the OpenAI dashboard — the hard backstop (see spend guardrails).
- App config (model ids, extraction version, concurrency caps) also env-driven, with defaults in code; no config service.

## Database migrations

`goose` migrations embedded in the binary, run explicitly via `opensight migrate` during deploy (not on startup — a bad migration shouldn't crash-loop the API). Both the app schema and the seeded `starter` plan row live in migrations.

## Local development

- `docker compose -f compose.dev.yml up`: Postgres + Temporal (+ UI). Go API/worker and Vite run natively (`air` for Go reload, `vite` dev server proxying `/api`).
- **`PromptRunner` stub mode** (env-selected): development and tests must not spend OpenAI money or wait on real searches. Two flavors: `stub` (canned, deterministic fixtures — a fake clinic-recommendation response with citations) and `replay` (recorded real `raw_response` payloads checked into `testdata/`). The analysis pipeline (05) develops almost entirely against replay data — real responses, zero cost, deterministic tests.
- Seed command: `opensight seed dev` creates a tenant, user, an applied business modeled on our design-partner clinic (e.g. Roots! Advanced Endodontics), prompts, and two runs of replay results, so every section of the UI has data on first boot.

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

PII stored: user emails and business profiles (public-figure practitioner names). **Hard product rule: no patient-identifiable data ever enters the system** — prompts are generic consumer queries, never real patient cases; the prompt-generation rules (03) and review-screen copy enforce it. This keeps the PDPA surface minimal by construction. Singapore PDPA applies to clinic customer data; hosting region (Hetzner SG vs EU) is a one-line compose choice — pick SG for data-locality optics with clinic customers. Add a plain-language privacy page before first paying customer.

## Open items deliberately left post-MVP

Self-serve signup, password reset emails (invite-only sidesteps both), billing (Stripe, per 01's billing-ready entitlements), competitor merge (05), metrics/Prometheus, multi-VPS.
