# Local Development

## Infrastructure

Start Postgres, Temporal, and Temporal UI:

```sh
docker compose -f compose.dev.yml up
```

Services:

- Postgres: `localhost:5432`
- Temporal: `localhost:7233`
- Temporal UI: `http://localhost:8233`

The Postgres container creates the `opensight`, `temporal`, and `temporal_visibility` databases. A one-shot Temporal admin-tools service applies Temporal schemas before the server starts. Temporal is capped at 20 active Postgres connections across persistence and visibility pools.

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
- `EXTRACTION_VERSION` defaults to `v1`
- `DEV_PROMPT_LIMIT` defaults to `3` for real-call smoke tests
- `PROMPT_CONCURRENCY` defaults to `2`
- `ANALYSIS_CONCURRENCY` defaults to `2`

Local development should use `stub` or `replay` unless a story explicitly requires a real OpenAI smoke test. For a low-cost real test, set `PROMPT_RUNNER_MODE=openai`, lower `DEV_PROMPT_LIMIT`, and override `OPENAI_RESPONSES_MODEL` to a cheaper web-search-capable model. `OPENAI_API_KEY` has no default and must stay in local uncommitted env only.
