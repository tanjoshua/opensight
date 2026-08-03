# OpenSight

Go application with an embedded React/Vite UI, backed by Postgres and Temporal. The static marketing site lives in `marketing/` and is deployed separately.

## Local setup

Prerequisites: Go 1.26.5, Node.js 22+, Docker with Compose, and the Stripe CLI.

```sh
cp .env.example .env
npm --prefix web ci
npm --prefix marketing ci
```

Fill in `.env` with Stripe sandbox credentials, a sandbox Starter Price, a pre-provisioned Billing Portal Configuration ID, and a Google OAuth client (`GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` — sign-in is Google-only; create a Web application client with `http://localhost:5173/auth/google/callback` as an authorized redirect URI). Apply the portal settings and forward webhooks:

```sh
go run ./cmd/opensight stripe portal-config
stripe listen --forward-to localhost:8080/webhooks/stripe
```

Put the webhook signing secret printed by Stripe into `STRIPE_WEBHOOK_SECRET`, then start the stack:

```sh
make up
```

This starts Postgres, Temporal, the API, worker, app UI, and marketing site, and applies database migrations. The command prints local URLs when everything is ready. Press `Ctrl+C` to stop native processes; run `make down` to stop Docker services.

For a ready-to-use local account, seed a comped tenant for your own Google account:

```sh
EMAIL=you@gmail.com make seed-dev
```

Sign in with that Google account at `http://localhost:5173/login` and complete onboarding normally. The default prompt runner is a no-cost stub.

## Operator commands

Run commands from source as shown below. Against a built or production deployment, invoke the same arguments with its binary instead, for example `/opensight migrate` or `docker compose exec app /opensight migrate`. Commands act on the environment supplied to that process; verify `DATABASE_URL` and Stripe configuration before running them in production.

```sh
# Apply pending database migrations. Serve and work do not migrate automatically.
go run ./cmd/opensight migrate

# Create an operator-provisioned tenant.
go run ./cmd/opensight tenant create --name "Acme Clinic"

# Add a login to that tenant. No password to set — the owner's first Google
# sign-in with this email links the account.
go run ./cmd/opensight user create \
  --tenant <tenant_id> \
  --email owner@example.com

# Create an active business from YAML/JSON, schedule monitoring, and trigger its first run.
go run ./cmd/opensight business create \
  --tenant <tenant_id> \
  --file path/to/business.yaml

# Apply the repository-owned settings to STRIPE_PORTAL_CONFIGURATION_ID.
go run ./cmd/opensight stripe portal-config
```

`tenant create` creates a **comped** Starter tenant with full access and no Stripe objects. It is intended for internal or operator-provisioned accounts, not normal paying customers. `business create` immediately starts monitoring and may incur OpenAI cost when `PROMPT_RUNNER_MODE=openai`.

## Development commands

```sh
make test              # unit tests; no infrastructure required
make test-db           # create/reset the integration-test database
make test-integration  # migrate and run DB-backed tests
make lint
make build             # writes bin/opensight
make proto             # regenerate protobuf/Connect code
make sqlc              # regenerate database query code
```

See [docs/dev.md](docs/dev.md) for configuration, integration tests, code generation, and lower-level stack commands. Product and technical plans live in [docs/prd.md](docs/prd.md) and [docs/design/](docs/design/).
