# Design 07 — Cross-Cutting Concerns

Depends on: all previous designs; closes their open questions.

## Auth and accounts

**Email + password with server-side sessions, hand-rolled in Go. Self-serve signup, gated on payment (08).**

- Passwords hashed with argon2id; sessions are random tokens, stored hashed, in a `sessions` table (Postgres), delivered as `HttpOnly, Secure, SameSite=Lax` cookies. Logout = delete row. No JWTs — nothing to revoke-by-expiry when sessions are just rows.
- Rationale: a managed provider (Clerk/Auth0) adds an external dependency and an eventual cost floor for what is, at this scale, ~200 lines of well-trodden Go. Self-hosted identity servers (Keycloak/Ory) are overkill on a 4GB VPS. Revisit when password reset, email verification, SSO or multi-user tenants stack up — that is the point where a managed provider starts paying for itself.
- **Signup** creates a tenant, a user and a subscription row in one transaction, then requires Stripe Checkout before any app surface opens (08). Taking the card first is what lets MVP ship without email verification: a completed charge is a stronger intent signal than a verified mailbox, and it removes the free-resource abuse that verification exists to stop.
- **Password reset is operator-run** (`opensight user set-password`) until a transactional email provider exists. This is the one knowingly incomplete part of self-serve; reset volume is the trigger to add one.
- Admin CLI account creation (`opensight user create --tenant …`) remains for operator-provisioned and comped tenants (08).
- CSRF: every RPC handler requires the Connect protocol header (`connect.WithRequireConnectProtocolHeader()`, `internal/api/rpc.go`) — a header a cross-origin form or bare browser navigation cannot set — combined with SameSite=Lax cookies this is sufficient for an RPC-only API. This guarantee depends on no method ever being declared `idempotency_level = NO_SIDE_EFFECTS`: Connect treats such a method as safe to accept over a header-less GET with the request encoded in the query string, which would bypass the header check entirely. `TestNoRPCIsSideEffectFree` (`internal/api/rpc_test.go`) walks the compiled proto descriptors and fails if any method is ever annotated that way, so this can't regress silently as new RPCs are added.
- API rate limiting: Caddy-level per-IP limit on `/rpc/`; nothing fancier until abuse exists.

## Secrets and config

- Twelve-factor env vars, loaded from an `.env` file on the VPS (mode 600, outside the repo) referenced by docker-compose. No secret manager at this scale.
- Inventory: Postgres passwords, OpenAI API key, Stripe restricted API key and webhook signing secret (08), healthcheck ping URLs. The OpenAI key is a **project-scoped key** with a monthly budget cap set in the OpenAI dashboard — the hard backstop (see spend guardrails).
- App config (model ids, extraction version, concurrency caps) also env-driven, with defaults in code; no config service.

## Database migrations

`goose` migrations embedded in the binary, run explicitly via `opensight migrate` during deploy (not on startup — a bad migration shouldn't crash-loop the API).

Application SQL is generated with sqlc from `internal/store/queries/`; tests
reference a separate generated catalog in `internal/store/testqueries/`.
`make sqlc` regenerates both packages and `make check-sql` rejects SQL embedded
in non-generated Go. Migration files, migration tests that inspect SQL text,
the Docker database initializer, and the operator cost report are the explicit
raw-SQL boundaries.

Integration fixtures use the same pgx pool as the repository under test. Their
small test-only dispatcher maps opaque catalog IDs to generated sqlc methods;
it uses reflection only to preserve varied fixture parameter/result shapes
without duplicating hundreds of wrappers. A catalog coverage test guarantees
every dispatcher ID resolves to a generated method. Reflection is not used on
production query paths.

## Local development

- `make up`: Postgres + Temporal (+ UI) run in Docker, migrations run once, and the Go API/worker run natively with `air`; the script waits for `/healthz`, prints service links, then reports the API healthy. When the frontend is present, Vite also runs natively on a strict local port and proxies `/rpc`.
- `docker compose -f compose.dev.yml up`: still available for infrastructure-only debugging.
- **`PromptRunner` stub mode** (env-selected): development and tests must not spend OpenAI money or wait on real searches. Two flavors: `stub` (canned, deterministic fixtures — a fake clinic-recommendation response with citations) and `replay` (recorded real `raw_response` payloads checked into `testdata/`). The analysis pipeline (05) develops almost entirely against replay data — real responses, zero cost, deterministic tests.
- **Stripe sandbox locally** (08): the serving path always uses Stripe, so local development exercises the real Checkout, Portal and webhook boundary with `stripe sandbox create` plus `stripe listen --forward-to localhost:8080/webhooks/stripe`. Tests inject the in-memory `StubProvider` and never call Stripe.
- Seed command: `opensight seed dev` creates only a comped tenant and login account. The developer completes the normal onboarding flow to create the business profile and initial prompts, keeping the end-to-end onboarding path exercised during local development.

## Deployment

- Git repo (private, GitHub).
- CI (GitHub Actions): test + lint + build a single multi-stage Docker image (Go binary with embedded SPA) pushed to GHCR. CI re-runs `make proto` and `make sqlc`, runs the SQL-boundary check, and fails on any diff, so committed protobuf, Connect, and query code cannot drift from their schemas and catalogs.
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

The authenticated Privacy page is PDPA-aware plain-language product copy, not a compliance certification or substitute for legal terms. It accurately enumerates what the product stores: account email, password verifier and session records; business profile data; prompts and lineage; raw monitoring responses, citations and request metadata; derived analysis and metrics; and plan, schedule, run, usage and cost configuration. Payment is processed by Stripe: the product stores subscription state and Stripe identifiers, never card numbers or any payment credential.

**Hard usage boundary: users must never enter patient-identifiable data.** Prompts are generic consumer queries rather than patient cases; generation rules and review copy reinforce this boundary. The product does not claim automatic detection or prevention of every prohibited entry.

Monitoring prompts and relevant business context are sent to the OpenAI API and may use web search. Public copy qualifies that OpenAI handling depends on the applicable service agreement and deployed account settings; it does not invent promises about external retention or training.

The service is pre-production. Singapore hosting is planned before production, but the product must not claim a production region until FND-5 deploys and verifies it. Monitoring results are retained indefinitely to preserve trends and evidence unless later terms or policy specify otherwise; legal, security, or dispute-preservation needs can override ordinary deletion. Account and profile data is retained as needed to operate the service and customer relationship, with specific deletion or post-account terms confirmed through the customer's account representative rather than invented here.

## Open items deliberately left post-MVP

Email verification and password reset emails (08 — no transactional email provider yet), competitor merge (05), metrics/Prometheus, multi-VPS.
