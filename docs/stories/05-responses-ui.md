# Epic 05 — App Shell & Responses UI (WEB)

The SPA foundation and the one section Phase 1 ships: Responses (raw results). Phase 1.

---

## WEB-1 — SPA scaffold and app shell

As the developer, I want the Vite + React + TypeScript app scaffolded with the design system, so that pages can be built consistently.

- [x] `npx shadcn@latest init --preset bLTjNXma --template vite` in `web/` (style rhea, stone base + chart colors, Lucide, Roboto).
- [x] TanStack Query for all server state (no global store); react-router with routes `/overview`, `/prompts`, `/competitors`, `/responses`, `/setup`, `/onboarding`, `/login`.
- [x] App shell: nav for the five sections; unbuilt sections show a placeholder.
- [x] `web/src/{api,components,pages,lib}` structure; typed client generated from the proto schema (`web/src/gen`) via connect-query hooks; Vite dev server proxies `/rpc`.
- [x] Production build embedded into the Go binary and served (completes FND-4's placeholder).

Deps: FND-1 · Phase 1 · Ref: design 06 (Frontend stack and structure)

## WEB-2 — Runs and results endpoints

As the developer, I want the read API for runs and results, so that the Responses section has data.

- [x] `ResultService.ListRuns` — scheduled_for, status per run (visibility % joins in Phase 2).
- [x] `ResultService.ListResults` — filters: run, prompt, status; `limit`/`offset` pagination. (`mentioned` filter arrives Phase 2.)
- [x] `ResultService.GetResult` — response text, run/model metadata, request params, error, raw JSON on demand.
- [x] All tenant-scoped through SCH-4; `connect.Error` codes.

Deps: SCH-4, AUTH-3 · Phase 1 · Ref: design 06 (Endpoints — Responses, Runs)

## WEB-3 — Responses section (list)

As a clinic user, I want to browse all stored ChatGPT responses with filters and statuses, so that I can read exactly what ChatGPT says each week.

- [x] Filterable list by run and prompt; paginated.
- [x] Failed results show status + error inline; run-level status visible (completed/partial/failed).
- [x] Empty state via shadcn `Empty` when no runs exist yet.

Deps: WEB-1, WEB-2 · Phase 1 · Ref: design 06 (Section notes — Responses), PRD §7

## WEB-4 — Response drawer v1

As a clinic user, I want any response to open in a detail drawer, so that the raw evidence is always one click away.

- [x] Slide-over drawer rendering `GET /results/:id`: full answer text, prompt text, model + timestamp, raw JSON behind a toggle.
- [x] Built as the single shared `ResponseDrawer` component every later metric will open (the "every number is a door" contract; mention highlights/citations/sentiment layer on in Phase 2, INS-4).

Deps: WEB-3 · Phase 1 · Ref: design 06 (The one UI contract)

## WEB-5 — Run-in-progress state

As a clinic user, I want to see that a run is currently executing, so that a mid-run visit doesn't look broken or empty.

- [x] Responses section (and shell, lightly) show an in-progress indicator polling run status while a run is `running`.
- [x] Single-run accounts render sensibly (no degenerate trends exist yet in Phase 1; full degraded-state pass is INS-1).

Deps: WEB-3 · Phase 1 · Ref: design 06 (Degraded and empty states)
