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

- [ ] GitHub Actions: test + lint on every push/PR.
- [ ] Multi-stage Dockerfile: Go binary with embedded SPA static files (placeholder `web/dist` until WEB-1), pushed to GHCR on `main`.
- [ ] Image runs both `serve` and `work` modes via command override.

Deps: FND-1 · Phase 1 · Ref: design 07 (Deployment)

## FND-5 — Production VPS and deploy script

As the operator, I want the full stack running on a single Hetzner VPS behind Caddy with a one-command deploy, so that production exists.

- [ ] Hetzner VPS (Singapore region) provisioned; Docker + Compose installed; SSH hardened (key-only).
- [ ] Prod `compose.yml`: `app` (serve), `worker` (work), `postgres`, `temporal`, `temporal-ui` (bound to localhost, reached via SSH tunnel), `caddy` with auto-HTTPS on the product domain.
- [ ] Secrets in `.env` on the VPS, mode 600, outside the repo; inventory documented (Postgres passwords, session signing key, OpenAI key).
- [ ] Caddy per-IP rate limit on `/api/`.
- [ ] Deploy script: SSH → `docker compose pull && docker compose up -d` → `opensight migrate`; brief downtime accepted.
- [ ] Total footprint fits ~4GB RAM; verified after stack is up.

Deps: FND-3, FND-4 · Phase 1 · Ref: design 01 (D8), 07 (Secrets, Deployment, Auth — rate limiting)

## FND-6 — Error tracking

As the operator, I want unhandled errors from Go and React reported to Sentry, so that failures don't rely on me reading logs.

- [ ] Sentry (free tier) wired into the Go API/worker and the React app (React part completes with WEB-1).
- [ ] `docker logs` rotation configured on the VPS.
- [ ] A deliberate test error appears in Sentry from production.

Deps: FND-5 · Phase 1 · Ref: design 07 (Observability)
