# Local Development

## Infrastructure

Start the full local stack:

```sh
make up
```

`make up` starts Postgres, Temporal, and Temporal UI in Docker, waits for
Postgres and the Temporal namespace bootstrap, runs `opensight migrate`, then
starts the Go API and worker with reload. It checks `/healthz` before reporting
the API as healthy and prints service links. When the frontend lands, the same
command also starts the Vite dev server on `http://127.0.0.1:5173` and prints
its link if `web/package.json` exists. Press `Ctrl+C` to stop the native dev
processes; Docker infrastructure stays up.

Seed the local login account after the database is running and migrated —
sign-in is Google-only (design 07 "Auth and accounts"), so this needs a real
Google account address:

```sh
EMAIL=you@gmail.com make seed-dev
# or: export OPENSIGHT_DEV_EMAIL=you@gmail.com once, then just `make seed-dev`
```

Then sign in with that Google account at `http://localhost:5173/login` and
complete the normal onboarding flow. The command is idempotent and does not
create a business, prompts, or monitoring results.

Signing in locally needs a real Google OAuth client: create one in Google
Cloud Console (Web application), add
`http://localhost:5173/auth/google/callback` as an authorized redirect URI,
and put `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` in `.env` (see
`.env.example`).

Start Postgres, Temporal, and Temporal UI:

```sh
make dev-stack
```

Services:

- Postgres: `localhost:5432`
- Temporal: `localhost:7233`
- Temporal UI: `http://localhost:8233`

The Postgres container creates the `opensight`, `temporal`, and `temporal_visibility` databases. A one-shot Temporal admin-tools service applies Temporal schemas before the server starts. Temporal is capped at 20 active Postgres connections across persistence and visibility pools.

`make up` service link defaults can be overridden with `HTTP_ADDR`,
`TEMPORAL_UI_URL`, `FRONTEND_HOST`, and `FRONTEND_PORT`.

Stop Docker infrastructure:

```sh
make down
```

Reset local Docker data:

```sh
make dev-stack-reset
```

## Native Go

Run the API with reload:

```sh
go tool air -c .air.serve.toml
```

Run the worker with reload:

```sh
go tool air -c .air.work.toml
```

Without reload:

```sh
go run ./cmd/opensight serve
go run ./cmd/opensight work
go run ./cmd/opensight migrate
```

`opensight migrate` applies embedded goose migrations against `DATABASE_URL` and exits. The API and worker do not run migrations on startup; run the command explicitly after changing schema or during deploy.

Create an invite-only account:

```sh
go run ./cmd/opensight account create --name "Acme Clinic"
go run ./cmd/opensight account member add --account <account_id> --email owner@example.com --role owner
```

`account member add` creates or reuses a global user with no Google identity;
the person's first sign-in with that email at `/login` links it. No invitation
email or acceptance step is involved.

## Tests

`make test` runs the unit suite; it needs no infrastructure.

The DB-backed tests in `internal/store`, `internal/workflows`, and `internal/metrics` skip themselves unless `OPENSIGHT_STORE_TEST_DATABASE_URL` is set. They run against their own `opensight_test` database so a test run can't disturb local data:

```sh
make test-db           # drop and recreate opensight_test (needs make dev-stack)
make test-integration  # migrate opensight_test, then run the DB-backed tests
```

`make test-integration` migrates first, so it is safe to re-run; `make test-db` is only needed the first time or to reset. Override the target database with `TEST_DATABASE_URL=<dsn>`. CI runs both targets against a Postgres service container.

## Protobuf / Connect RPC codegen

The API contract is defined in `proto/opensight/v1/*.proto` and compiled with [buf](https://buf.build). After changing any `.proto` file, regenerate:

```sh
make proto
```

This runs `buf format -w`, `buf lint`, then `buf generate`, which writes Go structs + Connect handler interfaces to `internal/gen/opensight/v1/` and TypeScript messages + connect-query method descriptors to `web/src/gen/opensight/v1/`. Two of the four codegen plugins (`protoc-gen-es`, `protoc-gen-connect-query`) are npm-hosted binaries resolved from `web/node_modules/.bin`, so `npm install` in `web/` must have been run at least once before `make proto` will work.

Regenerate database queries after changing a catalog or migration:

```bash
make sqlc
make check-sql
```

Production queries and metrics share `internal/store/queries/` and generate to
`internal/store/sqlc/`, using the embedded Goose migrations as the schema
source. `make check-sql` keeps SQL literals out of production Go. Integration
tests are exempt: they share one pgx pool with the stores they exercise and run
their fixture and assertion SQL inline, so the SQL is readable where it is used.

All generated output is committed — CI re-runs `make proto` and `make sqlc`
and fails the build on any diff, so checked-in generated code cannot drift from
its protobuf schemas, migrations, or query catalogs.

## Config

Runtime config is env-driven with development-safe defaults:

- `OPENSIGHT_ENV` defaults to `dev`; when it is `dev`, session cookies are set without the `Secure` attribute so login works over plain-HTTP local dev (prod runs behind Caddy TLS, where `Secure` is set)
- `HTTP_ADDR` defaults to `:8080`
- `DATABASE_URL` defaults to `postgres://opensight:opensight@localhost:5432/opensight?sslmode=disable`
- `APP_DB_MAX_OPEN_CONNS` defaults to `10`
- `TEMPORAL_ADDRESS` defaults to `localhost:7233`
- `TEMPORAL_NAMESPACE` defaults to `default`
- `TEMPORAL_TASK_QUEUE` defaults to `opensight`
- `PROMPT_RUNNER_MODE` defaults to `stub`; valid values are `stub`, `replay`, `openai`
- `OPENAI_RESPONSES_MODEL` defaults to `chat-latest`
- `OPENAI_ANALYSIS_MODEL` defaults to `gpt-5.6-luna`
- `OPENAI_ONBOARDING_MODEL` defaults to `gpt-5.6-terra` (quality-sensitive business-profile research, design 03)
- `OPENAI_QUESTIONS_MODEL` defaults to `gpt-5-mini` — a cheap non-reasoning model, since on-demand customer-question generation (design 03) does no research
- `PROMPT_CONCURRENCY` defaults to `2`
- `VISIBILITY_ASSESSOR_MODES` optionally overrides the typed rollout registry as comma-separated `assessor=DISABLED|SHADOW|ACTIVE` entries. Defaults: `search-access=ACTIVE,influential-source=SHADOW,tracked-topic=SHADOW`.
- `APP_BASE_URL` has no default and must be an absolute `http`/`https` URL; local `.env` should set `http://localhost:5173`
- `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_STARTER_MONTHLY`, `STRIPE_PORTAL_CONFIGURATION_ID` have no default and are required by `opensight serve`; other commands validate only the settings they use

Local development should use `stub` or `replay` unless a story explicitly requires a real OpenAI smoke test. For a low-cost real test, set `PROMPT_RUNNER_MODE=openai` and override `OPENAI_RESPONSES_MODEL` to a cheaper web-search-capable model. `OPENAI_API_KEY` has no default and must stay in local uncommitted env only.

The serving path always uses Stripe. For local development, run `stripe sandbox create`, provision one Portal Configuration in that sandbox, and put its `bpc_...` id in `.env` as `STRIPE_PORTAL_CONFIGURATION_ID`. Set `STRIPE_SECRET_KEY`, `STRIPE_PRICE_STARTER_MONTHLY`, and `APP_BASE_URL`, then run `opensight stripe portal-config` to apply the repo-owned settings to that exact configuration. Forward webhooks with `stripe listen --forward-to localhost:8080/webhooks/stripe` and set its signing secret as `STRIPE_WEBHOOK_SECRET`. Automated tests still use the injected in-memory provider and make no network calls. Production authenticates with a restricted key (`rk_`), never a secret key — see the go-live checklist in design 08.
