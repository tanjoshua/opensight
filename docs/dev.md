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

Seed the local login account after the database is running and migrated:

```sh
make seed-dev
```

Log in with `dev@opensight.local` and password `opensight-dev`, then complete
the normal onboarding flow. The command is idempotent and does not create a
business, prompts, or monitoring results.

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
go run ./cmd/opensight tenant create --name "Acme Clinic"
go run ./cmd/opensight user create --tenant <tenant_id> --email owner@example.com
```

Pipe a password with `--password-stdin` to set the initial password yourself; otherwise `user create` generates one and prints it once.

## Protobuf / Connect RPC codegen

The API contract is defined in `proto/opensight/v1/*.proto` and compiled with [buf](https://buf.build). After changing any `.proto` file, regenerate:

```sh
make proto
```

This runs `buf format -w`, `buf lint`, then `buf generate`, which writes Go structs + Connect handler interfaces to `internal/gen/opensight/v1/` and TypeScript messages + connect-query method descriptors to `web/src/gen/opensight/v1/`. Two of the four codegen plugins (`protoc-gen-es`, `protoc-gen-connect-query`) are npm-hosted binaries resolved from `web/node_modules/.bin`, so `npm install` in `web/` must have been run at least once before `make proto` will work.

All generated output is committed — CI re-runs `make proto` and fails the build on any diff, so the checked-in generated code and the `.proto` schema can never drift apart.

## Config

Runtime config is env-driven with development-safe defaults:

- `OPENSIGHT_ENV` defaults to `dev`; when it is `dev`, session cookies are set without the `Secure` attribute so login works over plain-HTTP local dev (prod runs behind Caddy TLS, where `Secure` is set)
- `HTTP_ADDR` defaults to `:8080`
- `DATABASE_URL` defaults to `postgres://opensight:opensight@localhost:5432/opensight?sslmode=disable`
- `APP_DB_MAX_OPEN_CONNS` defaults to `10`
- `APP_DB_MAX_IDLE_CONNS` defaults to `5`
- `TEMPORAL_ADDRESS` defaults to `localhost:7233`
- `TEMPORAL_NAMESPACE` defaults to `default`
- `TEMPORAL_TASK_QUEUE` defaults to `opensight`
- `PROMPT_RUNNER_MODE` defaults to `stub`; valid values are `stub`, `replay`, `openai`
- `OPENAI_RESPONSES_MODEL` defaults to `chat-latest`
- `OPENAI_ANALYSIS_MODEL` defaults to `gpt-5.6-luna`
- `DEV_PROMPT_LIMIT` defaults to `3` for real-call smoke tests
- `PROMPT_CONCURRENCY` defaults to `2`

Local development should use `stub` or `replay` unless a story explicitly requires a real OpenAI smoke test. For a low-cost real test, set `PROMPT_RUNNER_MODE=openai`, lower `DEV_PROMPT_LIMIT`, and override `OPENAI_RESPONSES_MODEL` to a cheaper web-search-capable model. `OPENAI_API_KEY` has no default and must stay in local uncommitted env only.
