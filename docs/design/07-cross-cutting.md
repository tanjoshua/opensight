# Design 07 — Cross-Cutting Concerns

Depends on: all previous designs; closes their open questions.

## Auth and accounts

**Google is the only sign-in method. Server-side sessions represent a global user identity; account access comes from memberships. Self-serve account provisioning is gated on payment (08).**

- Sign-in is a plain HTTP redirect flow, not an RPC: `GET /auth/google/start` mints a CSRF state token and a PKCE verifier, stashes both in a short-lived cookie, and redirects to Google's consent screen; `GET /auth/google/callback` verifies state, exchanges the code, and resolves the global user (`internal/api/google_auth.go`). The exchanged ID token's claims are read without a signature check — safe specifically because the token arrives over the direct, TLS- and client-secret-authenticated channel to Google's token endpoint, which OpenID Connect Core 1.0 §3.1.3.7 permits — but `aud`, `iss`, and `exp` are still checked explicitly.
- Identity resolution on callback: match globally by `users.google_sub`; on miss, match the verified email (`citext UNIQUE`) and link the sub. This links a pending member pre-added by email without duplicating the user. If the resolved user has no memberships, provision an unpaid Starter account, subscription, and owner membership transactionally. One membership forwards directly to that account; multiple memberships land on `/accounts`.
- Sessions are unchanged: random opaque tokens, stored hashed, in a `sessions` table (Postgres), delivered as `HttpOnly, Secure, SameSite=Lax` cookies. Logout = delete row. No JWTs — nothing to revoke-by-expiry when sessions are just rows.
- Rationale: Google has already verified the address, so there is nothing left for us to verify and no password to reset — the two gaps that used to be the trigger for revisiting a managed identity provider are gone by construction, not deferred.
- A never-paid account still requires Stripe Checkout before spend-triggering product actions (08). Owners may start Checkout; non-owners see that an owner must activate it.
- `opensight account member add --account … --email … --role …` creates or reuses a global user, inserts the membership immediately, and leaves `google_sub` unset until first sign-in. OpenSight sends no invitation email and has no invitation token or acceptance step; the UI labels an unlinked identity pending.
- An authenticated user may create another account. It starts as unpaid Starter with the creator as owner and routes to billing. Account membership grants access to every business in the account while each account has one business.
- CSRF: every RPC handler requires the Connect protocol header (`connect.WithRequireConnectProtocolHeader()`, `internal/api/rpc.go`) — a header a cross-origin form or bare browser navigation cannot set — combined with SameSite=Lax cookies this is sufficient for an RPC-only API. This guarantee depends on no method ever being declared `idempotency_level = NO_SIDE_EFFECTS`: Connect treats such a method as safe to accept over a header-less GET with the request encoded in the query string, which would bypass the header check entirely. `TestNoRPCIsSideEffectFree` (`internal/api/rpc_test.go`) walks the compiled proto descriptors and fails if any method is ever annotated that way, so this can't regress silently as new RPCs are added. `/auth/google/*` needs no such guard: it carries no session to forge, and its own CSRF protection is the state+PKCE cookie pair.
- API rate limiting: Cloudflare-level per-IP limit on `/rpc/` and `/webhooks/stripe` (dashboard.opensight.app is proxied through Cloudflare — see FND-5/`infra/README.md`); nothing fancier until abuse exists.

Account routes use `/a/:accountSlug/...`, and account-scoped RPCs send the same stable slug in `X-OpenSight-Account-Slug`. The server verifies membership on every request rather than trusting a browser-selected account. A slug is generated once from the initial name plus an eight-character UUID suffix, is unique, and does not change when the display name changes. Role changes and membership removal therefore apply on the next request without invalidating the global session.

## Secrets and config

- Twelve-factor env vars. The `.env` file on the VPS (mode 600, outside the repo) is **rendered by Ansible from SOPS-encrypted values checked into the repo** (`infra/inventory/group_vars/opensight/secrets.sops.yml`, age-encrypted), not hand-placed — see `infra/README.md`. No secret manager at this scale.
- Inventory: Postgres passwords, OpenAI API key, Stripe restricted API key and webhook signing secret (08), Google OAuth client secret, a GHCR pull token. The OpenAI key is a **project-scoped key** with a monthly budget cap set in the OpenAI dashboard — the hard backstop (see spend guardrails).
- App config (model ids, extraction version, concurrency caps) also env-driven, with defaults in code; no config service.

## Database migrations

`goose` migrations embedded in the binary, run explicitly via `opensight migrate` during deploy (not on startup — a bad migration shouldn't crash-loop the API).

Application SQL is generated with sqlc from `internal/store/queries/`.
`make sqlc` regenerates the package and `make check-sql` rejects SQL embedded
in non-generated production Go. Migration files, the Docker database
initializer, and the operator cost report are the explicit raw-SQL boundaries.

Tests are exempt from that rule: integration fixtures and assertions run plain
SQL through the same pgx pool as the repository under test, written inline at
the call site so a test reads top to bottom without a catalog lookup.

## Local development

- `make up`: PostgreSQL runs in Docker; migrations run once; the unified Go app runs natively with `air`; Vite and the marketing server run when present.
- `docker compose -f compose.dev.yml up`: still available for infrastructure-only debugging.
- **`PromptRunner` stub mode** (env-selected): development and tests must not spend OpenAI money or wait on real searches. Two flavors: `stub` (canned, deterministic fixtures — a fake clinic-recommendation response with citations) and `replay` (recorded real `raw_response` payloads checked into `testdata/`). The analysis pipeline (05) develops almost entirely against replay data — real responses, zero cost, deterministic tests.
- **Stripe sandbox locally** (08): the serving path always uses Stripe, so local development exercises the real Checkout, Portal and webhook boundary with `stripe sandbox create` plus `stripe listen --forward-to localhost:8080/webhooks/stripe`. Automated tests use narrow package-local fakes and never call Stripe.
- Seed command: `opensight seed dev` creates only a comped account, a global user, and an owner membership. The developer completes the normal onboarding flow to create the business profile and initial prompts, keeping the end-to-end onboarding path exercised during local development.

## Deployment

- Git repo (private, GitHub).
- CI (GitHub Actions): test + lint + build a single multi-stage Docker image (Go binary with embedded SPA) pushed to GHCR. CI re-runs `make proto` and `make sqlc`, runs the SQL-boundary check, and fails on any diff, so committed protobuf, Connect, and query code cannot drift from their schemas and catalogs.
- Deploy = Ansible: render configuration, pull images, start PostgreSQL, stop `app`, run `docker compose run --rm app migrate`, start `app` and Caddy, then verify `/healthz`. The stack is `app`, `postgres`, and `caddy`.

## Backups and recovery

- Nightly `pg_dump` of the single `opensight` database includes product and River state, encrypted by restic and shipped offsite.
- The `.env` file is included in the restic set (it's the only non-reproducible thing outside Postgres).
- **Restore drill is part of MVP acceptance**: on a scratch VPS, restore last night's dump, `docker compose up`, confirm the app serves and schedules resume. An untested backup is a hope, not a backup. Recovery point of ≤24h is fine — worst case a week's run re-executes (idempotency keys make that safe, 04).

## Observability and spend guardrails

Kept deliberately minimal for MVP:

- **Errors**: Sentry free tier for Go + React (or self-hosted GlitchTip later if cost/data-locality demands; free tier is within budget policy).
- **Logs**: structured `slog` JSON to stdout → `docker logs` with rotation. No Loki/ELK; grep is fine at this scale.
- **Queue debugging**: structured logs plus the `river_job` query in design 04; no dashboard service.
- **Improve health**: `site_audits` records crawl health; failed assessment jobs remain visible in `river_job` and structured logs.
- **Spend guardrail**: the OpenAI dashboard monthly budget cap on the project-scoped key is the hard backstop — no in-app circuit breaker. The per-account cost query (04) still exists for unit economics, run ad hoc.

## Data protection (light-touch, noted not lawyered)

The authenticated Privacy page is PDPA-aware plain-language product copy, not a compliance certification or substitute for legal terms. It accurately enumerates what the product stores: account email, Google account identifier and session records — never a password, since Google handles sign-in entirely; business profile data; prompts and lineage; raw monitoring responses, citations and request metadata; derived analysis and metrics; and plan, schedule, run, usage and cost configuration. Payment is processed by Stripe: the product stores subscription state and Stripe identifiers, never card numbers or any payment credential.

**Hard usage boundary: users must never enter patient-identifiable data.** Prompts are generic consumer queries rather than patient cases; generation rules and review copy reinforce this boundary. The product does not claim automatic detection or prevention of every prohibited entry.

Monitoring prompts and relevant business context are sent to the OpenAI API and may use web search. Public copy qualifies that OpenAI handling depends on the applicable service agreement and deployed account settings; it does not invent promises about external retention or training.

The service is pre-production. Singapore hosting is planned before production, but the product must not claim a production region until FND-5 deploys and verifies it. Monitoring results are retained indefinitely to preserve trends and evidence unless later terms or policy specify otherwise; legal, security, or dispute-preservation needs can override ordinary deletion. Account and profile data is retained as needed to operate the service and customer relationship, with specific deletion or post-account terms confirmed through the customer's account representative rather than invented here.

## Open items deliberately left post-MVP

Admin account management (including changing comp status), competitor merge (05), metrics/Prometheus, multi-VPS.
