# Development

## Local stack

`make up` starts PostgreSQL in Docker, applies application and River migrations, then runs the unified Go app with reload plus Vite and the marketing server. It waits for `/healthz` and prints local links. `Ctrl+C` stops native processes; `make down` stops PostgreSQL.

For a local login, configure a Google OAuth web client with `http://localhost:5173/auth/google/callback`, then seed a comped account:

```sh
EMAIL=you@gmail.com make seed-dev
```

The command is idempotent and deliberately does not create a business or results; complete normal onboarding in the UI.

Lower-level commands:

```sh
make dev-stack       # PostgreSQL only
make dev-serve       # unified app with reload
make down
make dev-stack-reset # remove local Docker data
```

The server starts River before HTTP. There is no worker command, local worker reload process, queue UI, or queue-specific database.

## Tests

```sh
make test
make lint
make check-sql
```

Database-backed tests use an isolated database:

```sh
make test-db
make test-integration
```

`make test-db` drops and recreates only `opensight_test`. `make test-integration` applies Goose and River migrations, runs the store/API/metrics/operation suites, then runs River integration separately so account-count-sensitive tests do not share concurrent fixtures. Override the database with `TEST_DATABASE_URL=<dsn>`.

## Generated code

The protobuf contract lives in `proto/opensight/v1`. After changing it:

```sh
make proto
```

This formats, lints, and generates committed Go and TypeScript code. The npm-hosted plugins require `npm ci` in `web/` first.

After changing migrations or SQL catalogs:

```sh
make sqlc
make check-sql
```

CI regenerates both surfaces and rejects drift.

## Frontend

```sh
cd web
npm run typecheck
npm run lint
npm run build
```

## Runtime configuration

- `OPENSIGHT_ENV=dev`
- `HTTP_ADDR=:8080`
- `DATABASE_URL=postgres://opensight:opensight@localhost:5432/opensight?sslmode=disable`
- `APP_DB_MAX_OPEN_CONNS=10`
- `PROMPT_RUNNER_MODE=stub` (`stub`, `replay`, or `openai`)
- `LLM_CONCURRENCY=2`, shared by jobs and synchronous question generation
- model variables in `internal/config`

OpenAI mode requires `OPENAI_API_KEY`. `serve` also requires the Stripe, Google OAuth, and `APP_BASE_URL` values documented in `.env.example`. Local development should use `stub` or `replay` unless a real provider smoke test is intentional.

Queue inspection uses structured app logs and the `river_job` query in [Design 04](design/04-monitoring.md). There is no dashboard service.
