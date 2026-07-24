# Design 06 — API and Frontend Structure

Depends on: [02 Data Model](02-data-model.md), [03 Onboarding](03-onboarding.md) (onboarding endpoints), [04 Monitoring](04-monitoring.md) (run status), [05 Analysis](05-analysis.md) (display filtering)

## API conventions

- REST/JSON under `/api/v1/`, served by the Go binary; the SPA is static files from the same binary, so no CORS in production (Vite dev server proxies `/api` locally).
- Auth middleware (design 07) resolves the session → tenant; **every handler receives tenant context and every repository call is tenant-scoped** — there is no unscoped query path.
- MVP has one business per tenant, but URLs carry `businessID` anyway (multi-location future, PRD §9). The API validates business→tenant ownership on every request.
- Errors: RFC 7807 problem+json. Pagination: `limit`/`offset`, only where lists can grow (results, competitors).
- All metrics are computed server-side in one shared `internal/metrics` package (SQL over 02's tables) — Overview, Prompts, and Competitors must never disagree because they computed visibility differently. Mention facts come only from `mentions`, and visibility counts only analyzed results (02/05): succeeded-but-unanalyzed results are excluded from the math and badged.

## Endpoints by section

Onboarding endpoints are already defined in 03. The five product sections (PRD §7):

```
# Overview
GET  /businesses/:id/overview        current visibility %, delta vs previous run,
                                     top keywords, top cited domains, top competitors,
                                     latest run status
GET  /businesses/:id/citations       ?domain, limit, offset; cited domains with
                                     response frequency, cited pages, associated
                                     prompts, subject splits, and result_ids

# Prompts
GET  /businesses/:id/prompts         active prompts + latest result summary + spark trend
POST /businesses/:id/prompts         add (409 if at plan.prompt_limit)
POST /prompts/:id/replace            body {text, confirmed: true} — retire + create (02)
GET  /prompts/:id                    prompt detail: full history of results, lineage links

# Competitors
GET  /businesses/:id/competitors     ?status filter; each with mention %, totals,
                                     avg order, per-prompt appearances
                                     (prompt text + result_ids), trend
                                     including zero-mention analyzed weeks,
                                     vs-self comparison
POST /businesses/:id/competitors     manual add {name, aliases?, website?}
                                     (source manual, initially tracked; visible
                                     with zero history before its first mention)
POST /competitors/:id/track          status → tracked
POST /competitors/:id/dismiss        status → dismissed
POST /competitors/:id/suggested-aliases/approve
                                     body {alias}; remove exact suggestion and
                                     append once to approved aliases
POST /competitors/:id/suggested-aliases/reject
                                     body {alias}; remove suggestion only
PATCH /competitors/:id               body {aliases}; approved aliases only

# Responses
GET  /businesses/:id/results         filters: run, prompt, mentioned, status; paginated
GET  /results/:id                    full record: response text, analysis, mentions,
                                     citations, run/model metadata, raw on demand

# Runs (feeds all trend charts)
GET  /businesses/:id/runs            run list: scheduled_for, status, visibility %

# Setup
GET  /businesses/:id                 full profile + read-only plan entitlements
PATCH /businesses/:id                partial profile merge; active-only (409 draft),
                                     complete merged profile remains valid and
                                     country remains a two-letter ISO code
```

In Phase 1, before analysis tables exist, the Responses endpoints expose runs,
prompt/result status, prompt text, response text, errors, request params, and raw
JSON on demand. Phase 2 enriches the same `GET /results/:id` response with
analysis, mentions, and citations.

## The one UI contract: every number is a door

PRD §6's "every metric links to the underlying response" is implemented as a single pattern, not per-feature plumbing: **every aggregate the API returns carries the `result_ids` behind it**, and every stat component in the UI is clickable → opens the **Response drawer** (a slide-over rendering `GET /results/:id`: answer text with business/competitor mentions highlighted and **inline citation markers rendered at their annotation spans**, sentiment + excerpts, the citation list, model + timestamp, raw JSON behind a toggle). Charts deep-link the same way: clicking a week on a trend line opens Responses filtered to that run. One drawer component, used everywhere, is the whole traceability story.

## Section notes

- **Overview** — headline visibility stat + weekly trend line, then three compact panels (themes, cited domains, competitors). Competitor panel shows tracked competitors plus top-3 discovered by coverage (the 05 display filter), with a "N discovered → triage" link into Competitors. The weekly trend renders **prompt-set-change markers** (derived from prompt created/retired dates) so a prompt change never reads as a visibility change. Its header always links to **How we measure**, including before any analyzed data exists.
- **Prompts** — table of 20 with per-prompt: mentioned? order? sentiment, sparkline across runs. **Replace flow (PRD §4)**: modal states exactly what happens — "history for the old prompt stays viewable; the new prompt starts a fresh trend" — and the API requires `confirmed: true`, so the warning is structurally unskippable. Retired prompts remain reachable from a prompt's lineage ("replaced X on date").
- **Competitors** — triage-first: discovered list ranked by response coverage ("in 7 of 20 responses") with one-click track/dismiss; tracked list with the PRD comparison stats vs self, prompt appearances, and weekly trend lines that include zero-mention analyzed weeks. A manual add starts tracked and remains visible with zero metrics and an empty prompt history until it is mentioned; its trend still carries zero points for analyzed runs. Dismissed collapsed but recoverable (data was never deleted, per 02). Every competitor status exposes pending LLM-`suggested_aliases` for one-click approval or rejection (05). Approval promotes that exact value to `aliases`; rejection removes the suggestion without creating a deny-list.
- **Responses** — filterable list (by run, prompt, mention, status). Failed results show status + error; succeeded-but-unanalyzed show a "not yet analyzed" badge (05's soft-failure posture made visible instead of silently miscounted).
- **Setup** — active-business profile editor with dirty/save feedback, repeatable structured controls for aliases, services, and practitioner name/role pairs, a prompt-management entry point, approved competitor-alias editing across every status, and read-only plan display. Each alias occupies its own control, so punctuation such as commas remains part of the value. Draft businesses redirect to onboarding; there is no regenerate action after activation (03). The client sends only changed profile fields. Profile PATCH merges omitted fields against the current tenant-owned row for validation, treats omitted or `null` website as unchanged and an empty website as clear, then atomically updates only the supplied columns so a concurrent disjoint edit cannot restore stale values. Competitor alias edits are trim-normalized and case-insensitively deduplicated; they never mutate suggestions or history.
- **How we measure** — authenticated trust page explaining that OpenSight uses the OpenAI Responses API with web search and business-location context as a proxy, not a capture of chatgpt.com. API requests have no history, memory, or personalization and routing can differ; the API-reported model is stored per response. Visibility is mentions divided by analyzed valid responses, excluding failures and not-yet-analyzed responses. The page emphasizes traceability and that results are neither accuracy nor future-performance guarantees. It is linked from the Overview header, response-drawer model line, app footer, and Privacy.
- **Privacy** — authenticated plain-language summary of actual stored account, profile, prompt, raw response, derived analysis, and operational configuration data; the patient-data usage boundary; qualified OpenAI processing; verified-vs-planned hosting; and retention. It is linked from the app footer and How we measure.

## Frontend stack and structure

Vite + React + TypeScript. Deliberately small kit, consistent with solo maintenance:

- **TanStack Query** for all server state; no Redux/global store — server is the source of truth and the app is read-heavy.
- **react-router** with routes mirroring the five sections: `/overview`, `/prompts`, `/competitors`, `/responses`, `/setup`, plus `/onboarding` for the draft flow.
- **Tailwind + shadcn/ui** for components, initialized with preset `bLTjNXma` (style `rhea`, stone base + chart colors, Lucide icons, Roboto): `npx shadcn@latest init --preset bLTjNXma --template vite`. Charts use **shadcn's `Chart` component** (wraps Recharts, themed by the preset's chart-color variables) — no separately styled charting layer.

```
web/src/
├── api/            typed client + TanStack Query hooks (one module per section)
├── components/     ResponseDrawer, TrendChart, StatCard, MentionBadge, …
├── pages/          overview/ prompts/ competitors/ responses/ setup/ onboarding/
└── lib/
```

## Degraded and empty states (specified now, because they're the first thing users see)

- **First run in progress** (post-onboarding): every section shows a progress state polling run status, not empty charts — this is the honest version of PRD success criterion 4.
- **Partial run**: banner on Overview ("18 of 20 prompts succeeded this week") linking to failed results; visibility math already excludes failures (04).
- **Single data point**: trend components render a labeled single point, not a degenerate line — most users' second session is week one.

## Open questions (owned by 07)

- Session mechanics, login UX, and whether MVP has any self-serve signup or is invite/manual.
- Rate limiting on the API (probably just a reverse-proxy limit at MVP).
