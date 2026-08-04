# Epic 01 — Foundation & Infra (FND)

Everything needed before feature work: repo, dev environment, migrations tooling, CI, production host. Phase 1.

---

## FND-1 — Repo and monorepo scaffold

As the developer, I want a Go monorepo skeleton with the binary's run modes stubbed, so that every later story has a home and a build that passes.

- [x] Private GitHub repo `opensight` initialized; `docs/` (PRD, design, stories) committed.
- [x] Layout per design 01-D7: `cmd/opensight/`, `internal/{api,domain,store,workflows,llm}`, `web/` (placeholder), `docs/`.
- [x] `opensight serve`, `opensight work`, `opensight migrate` subcommands exist (can be no-ops) from one binary.
- [x] `go test ./...` and a linter (`golangci-lint`) run clean; Makefile or task runner targets for build/test/lint.
- [x] Structured `slog` JSON logging to stdout wired as the default logger.

Deps: — · Phase 1 · Ref: design 01 (D2, D7), 07 (Deployment, Observability)

## FND-2 — Local dev stack (Postgres + Temporal)

As the developer, I want `docker compose -f compose.dev.yml up` to give me Postgres and Temporal locally, so that the app runs natively against real infrastructure.

- [x] `compose.dev.yml`: Postgres (with `opensight`, `temporal`, and `temporal_visibility` databases created), Temporal single node backed by that Postgres with explicit admin-tools schema bootstrap, Temporal UI.
- [x] Temporal connection pool capped (~20) per the shared-instance guardrail.
- [x] Go API/worker run natively with `air` reload; documented in a `docs/dev.md` or README section.
- [x] App config via env vars with defaults in code (model ids, concurrency caps, DB URLs).

Deps: FND-1 · Phase 1 · Ref: design 01 (D4, D5), 07 (Local development)

## FND-3 — Migrations tooling

As the developer, I want goose migrations embedded in the binary and run via `opensight migrate`, so that schema changes are explicit, versioned, and never run on startup.

- [x] `goose` migrations embedded; `opensight migrate` applies them against `DATABASE_URL`.
- [x] Migrate is **not** invoked on app startup (a bad migration must not crash-loop the API).
- [x] First migration exists (can be trivial) proving up/down and embed both work.

Deps: FND-2 · Phase 1 · Ref: design 07 (Database migrations)

## FND-4 — CI to GHCR

As the developer, I want every push tested and `main` built into a deployable image, so that deploys are pull-and-restart.

- [x] GitHub Actions: test + lint on every push/PR.
- [x] Multi-stage Dockerfile: Go binary with embedded SPA static files (placeholder `web/dist` until WEB-1), pushed to GHCR on `main`.
- [x] Image runs both `serve` and `work` modes via command override.

Deps: FND-1 · Phase 1 · Ref: design 07 (Deployment)

## FND-5 — Production VPS and deploy script

As the operator, I want the full stack running on a single OVHcloud VPS behind Caddy with a one-command deploy, so that production exists.

IaC is config-only (`infra/`, Ansible + SOPS/age): the VPS is created by hand
in the OVH panel; the playbooks take it from bare Ubuntu to running. See
`infra/README.md` for the full runbook.

- [ ] OVHcloud VPS (Singapore region, min 4GB RAM) provisioned; DNS (`dashboard.opensight.app`, proxied through Cloudflare) points at it. *(requires a live VPS + Cloudflare account — not verifiable in this session)*
- [x] `infra/provision.yml` (`make infra-provision`): installs Docker + Compose, creates a `deploy` user with key-only SSH, disables SSH password/root login, UFW default-deny with 22/80/443 open (80/443 restricted to Cloudflare's IP ranges), unattended-upgrades, a swapfile.
- [x] `infra/deploy.yml` (`make infra-deploy`) renders prod `compose.yml`: `app` (serve), `worker` (work), `postgres`, `temporal`, `temporal-schema`/`temporal-namespace` bootstrap, `temporal-ui` (bound to `127.0.0.1:8233`, reached via `make infra-tunnel`), `caddy` with auto-HTTPS on `dashboard.opensight.app`.
- [x] Secrets rendered to `.env` on the VPS (mode 600, outside the repo) by Ansible from `infra/inventory/group_vars/opensight/secrets.sops.yml` (SOPS + age, encrypted in the repo); full inventory in `infra/README.md`. *(checked in as an unencrypted placeholder — no real age key exists yet; see `infra/README.md` "Secrets" for the exact commands to generate one)*
- [ ] `/rpc/` and `/webhooks/stripe` rate limits configured in Cloudflare (dashboard proxied, SSL/TLS mode Full (Strict)). *(requires a live Cloudflare account — not verifiable in this session)*
- [x] `make infra-deploy`: `docker login ghcr.io` → `docker compose pull` → bring up `postgres`/`temporal` and wait for the namespace bootstrap → `docker compose run --rm app migrate` (explicit and separate from `serve`/`work` startup) → `docker compose up -d` → verify `https://dashboard.opensight.app/healthz`; brief downtime accepted. Rollback: `make infra-deploy TAG=sha-<short>`, no rebuild.
- [ ] Total footprint fits ~4GB RAM; verified after stack is up. *(requires a live VPS — not verifiable in this session)*

Deps: FND-3, FND-4 · Phase 1 · Ref: design 01 (D8), 07 (Secrets, Deployment, Auth — rate limiting)

## FND-6 — Error tracking

As the operator, I want unhandled errors from Go and React reported to Sentry, so that failures don't rely on me reading logs.

- [ ] Sentry (free tier) wired into the Go API/worker and the React app (React part completes with WEB-1).
- [ ] `docker logs` rotation configured on the VPS.
- [ ] A deliberate test error appears in Sentry from production.

Deps: FND-5 · Phase 1 · Ref: design 07 (Observability)
