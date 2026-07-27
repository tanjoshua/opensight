# Design 06 — API and Frontend Structure

Depends on: [02 Data Model](02-data-model.md), [03 Onboarding](03-onboarding.md) (onboarding endpoints), [04 Monitoring](04-monitoring.md) (run status), [05 Analysis](05-analysis.md) (display filtering)

## API conventions

- Protobuf schema under `proto/opensight/v1/` (package `opensight.v1`), served over **Connect RPC** at `/rpc` by the Go binary; the SPA is static files from the same binary, so no CORS in production (Vite dev server proxies `/rpc` locally).
- `make proto` (buf format + lint + generate) regenerates code from the `.proto` files: Go structs + Connect handler interfaces into `internal/gen/opensight/v1/`, and TS messages + connect-query method descriptors into `web/src/gen/opensight/v1/`. Both trees are committed — CI re-runs `make proto` and fails on any diff, so generated code can never drift from the schema.
- Auth: a Connect interceptor (`sessionInterceptor`, `internal/api/rpc.go`) resolves the session cookie into tenant context for every procedure except a small `publicProcedures` allowlist (just `AuthService.Login`) — default-deny, keyed by generated procedure constants so a renamed/removed RPC breaks the build instead of silently changing access. **Every handler receives tenant context and every repository call is tenant-scoped** — there is no unscoped query path.
- MVP has one business per tenant, but requests carry `business_id` anyway (multi-location future, PRD §9). The API validates business→tenant ownership on every request.
- Errors: a `connect.Error` from a small fixed set of codes (`Unauthenticated`, `NotFound`, `FailedPrecondition`, `ResourceExhausted`, `AlreadyExists`, `InvalidArgument`, `Internal`) carrying a client-safe message — the real error always goes to `slog`, never the client. Pagination: `limit`/`offset` request fields plus a `Paging` response message, only where lists can grow (results, competitors, citations).
- All metrics are computed server-side in one shared `internal/metrics` package (SQL over 02's tables) — Overview, Prompts, and Competitors must never disagree because they computed visibility differently. Mention facts come only from `mentions`, and visibility counts only analyzed results (02/05): succeeded-but-unanalyzed results are excluded from the math and badged.

## Services and RPCs

Onboarding RPCs are already defined in 03. Seven services, 23 RPCs total, covering the five product sections (PRD §7) plus auth and onboarding:

```
# AuthService
Login(email, password)                     → user, tenant — the only public procedure
Logout()
GetMe()                                     → user, tenant, businesses (section picker), plan prompt_limit

# BusinessService (onboarding + Setup)
CreateBusiness(name, website)               → draft business summary; starts onboarding
GetBusiness(business_id)                    → full profile + read-only plan entitlements
UpdateBusiness(business_id, fields...)      → partial profile merge; active-only,
                                              complete merged profile remains valid and
                                              country remains a two-letter ISO code
GetProposal(business_id)                    → onboarding profile-proposal status, polled while generating
RegenerateProposal(business_id)             → restarts profile generation
ApplyProposal(business_id, payload)         → activates the business, inserts prompts from
                                              the edited proposal (02)

# OverviewService
GetOverview(business_id)                    → current visibility %, delta vs previous run, weekly
                                              trend + prompt-change markers, top keywords, top cited
                                              domains, top competitors (with their own trend), count
                                              of discovered-but-untriaged competitors, latest run

# CitationService
ListCitationSources(business_id,            → cited domains with response frequency, cited pages,
  domain?, limit, offset)                     associated prompts, subject splits, and result_ids

# PromptService
ListPrompts(business_id)                    → active prompts + latest result summary + spark trend
AddPrompt(business_id, text)                → ResourceExhausted if at plan.prompt_limit
GetPrompt(prompt_id)                        → prompt detail: full result history, lineage links
ReplacePrompt(prompt_id, text, confirmed)   → retire + create (02); confirmed must be true

# CompetitorService
ListCompetitors(business_id, status?,       → self baseline; each competitor with mention %, totals,
  limit, offset)                              avg order, per-prompt appearances (prompt text +
                                              result_ids), trend including zero-mention analyzed
                                              weeks, vs-self comparison
AddCompetitor(business_id, name,            → manual add (source manual, initially tracked; visible
  aliases?, website?)                         with zero history before its first mention)
SetCompetitorStatus(competitor_id, status)  → status → tracked or dismissed (one RPC for both)
ReviewSuggestedAlias(competitor_id,         → decision APPROVE: remove suggestion, append once to
  alias, decision)                            approved aliases; REJECT: remove suggestion only
UpdateCompetitorAliases(competitor_id,      → replace approved aliases only
  aliases)

# ResultService (Runs, feeds all trend charts)
ListRuns(business_id)                       → run list: scheduled_for, status, visibility %,
                                              per-run expected/succeeded/failed/analyzed counts,
                                              plus a best-effort next_run_at from the business's
                                              Temporal Schedule (unset if unreachable/missing —
                                              never fails the request)
ListResults(business_id, run_id?,           → filters: requested result ids, run, prompt,
  result_ids?, prompt_id?, status?,           mentioned, status; paginated; result_ids preserve
  mentioned?, limit, offset)                  caller order after business/tenant scoping
GetResult(result_id, include_raw)           → full record: response text, analysis, mentions,
                                              citations, run/model metadata, raw JSON on demand
```

## Schema design rules

- Instants (`started_at`, `completed_at`, …) are `google.protobuf.Timestamp`; date-only values (`scheduled_for`) stay plain `YYYY-MM-DD` strings — a calendar date isn't an instant and doesn't need timezone-aware wire semantics.
- Every closed domain set is a real proto enum with a `*_UNSPECIFIED = 0` zero value (e.g. `BusinessStatus`, `RunStatus`, `Sentiment`); fields backed by an open or non-domain string stay a plain `string` (`Run.platform`, `Plan.run_interval`) rather than a false enum.
- PATCH-style partial updates use `optional` scalar fields (proto3 presence: omitted = unchanged) plus a `StringList` wrapper message (`common.proto`) for repeated fields, since a bare `repeated string` cannot distinguish "omitted, leave unchanged" from "present but empty, clear" from "present with values, replace". `UpdateBusinessRequest` is the canonical example: `optional string website` — omitted leaves it unchanged, present-and-empty clears it; `StringList aliases` — nil leaves it unchanged, present-with-zero-values clears it, present-with-values replaces it.
- Opaque JSON payloads (`PromptResult.request_json`, `raw_response_json`) are plain `string`, not `google.protobuf.Struct` — `Struct` encodes every number as `double`, which would silently mangle the int64 token counts in stored OpenAI response payloads.
- **Every number is a door**: every aggregate/summary message carries `repeated string result_ids` under that exact field name — the analyzed-result ids backing that number, so the frontend can always drill from a stat to its evidence (see below).

## The one UI contract: every number is a door

PRD §6's "every metric links to the underlying response" is implemented as a single pattern, not per-feature plumbing: **every aggregate the API returns carries the `result_ids` behind it**, and every stat component in the UI is clickable → opens the **Response drawer** (a slide-over rendering `ResultService.GetResult`: answer text with business/competitor mentions highlighted and **inline citation markers rendered at their annotation spans**, sentiment + excerpts, the citation list, model + timestamp, raw JSON behind a toggle). Charts deep-link the same way: clicking a week on a trend line opens that run's detail page. One drawer component, used everywhere, is the whole traceability story.

## Section notes

- **Brief** (`/overview`) — the authenticated landing page and a concise visibility briefing. Its header names the exact latest analyzed run date and denominator. One adaptive **Visibility explorer** card has **Run snapshot** and **Over time** modes; changing its run or range affects only this card. Question changes and absence elsewhere use each question's latest analyzed responses, while cited sources, common themes, and competitors summarize evidence collected to date. The explorer keeps its chart as the dominant first-read object, with the latest visibility/date/denominator in a compact header and low-emphasis unoutlined mode/range toggles at the chart edge. With one analyzed run it defaults to Snapshot and does not render a fabricated future axis. With two runs it defaults to a straight before→after trend. With three or more it defaults to a straight-segment line over the last 12 runs; when history permits, Last 4, Last 12, and All ranges are available. Snapshot can select any analyzed run and states its exact date and mentioned/analyzed denominator. It shows horizontal comparison bars for the business plus up to three current Overview competitors that have data for that run, falling back to a mentioned/not-mentioned composition bar when none do. Every comparison value exposes its per-run `result_ids`. Over time plots exact observed values with no curve interpolation or gap-connecting and may include at most two current competitor lines. Selecting a chart point reveals its date, value, denominator, **View responses**, and **Open run** actions instead of navigating immediately; dated native controls provide equivalent keyboard and touch access. Prompt-set-change markers appear only after the first visible observed run and through the last visible run; a native keyboard-accessible annotation disclosure lists their dates and descriptions. A **What changed** section compares only the two latest analyzed points belonging to the same active prompt and classifies defensible transitions as newly visible, no longer visible, or still absent; prompts with fewer than two points have no baseline and are not classified. **Where to focus** presents latest absent questions, cited domains from evidence collected to date, and competitors from evidence collected to date as evidence to inspect, not recommendations. Its lists and the secondary common-themes list are capped at five items; links lead to the full Questions and Competitors sections. The competitor panel shows tracked competitors plus top-3 discovered by coverage (the 05 display filter), with a "N discovered → triage" link into Competitors. The header always links to **How we measure**, including before any analyzed data exists.
- **Questions** (`/prompts`) — semantic desktop table of monitored prompts with per-prompt: mentioned?, order, sentiment, and sparkline across runs. Below the `md` breakpoint it reflows into cards instead of horizontally scrolling: the question, latest metrics, response history, latest-response action, and question-detail action remain visible with touch targets at least 44px high. **Replace flow (PRD §4)**: modal states exactly what happens — "history for the old prompt stays viewable; the new prompt starts a fresh trend" — and the API requires `confirmed: true`, so the warning is structurally unskippable. Retired prompts remain reachable from a prompt's lineage ("replaced X on date").
- **Competitors** — triage-first: a compact discovered list is ranked by response coverage ("in 7 of 20 responses") with one-click track/dismiss. Tracked competitors use a comparison table on desktop and summary cards on small screens; selecting one opens a single detail panel with the PRD comparison stats, prompt appearances, approved and suggested aliases, evidence set, and a larger weekly trend that includes zero-mention analyzed weeks. Dated native controls below the trend provide keyboard-accessible navigation to each run. A manual add starts tracked and remains selectable with zero metrics and an explicit empty-history state until it is mentioned; its trend still carries zero points for analyzed runs. Dismissed stays collapsed but recoverable (data was never deleted, per 02). Track, dismiss, and restore actions give local success feedback. Undo is offered only for the true inverse transitions between tracked and dismissed; triaging a discovered competitor cannot offer Undo because the status API deliberately cannot restore `discovered`. Every competitor status exposes pending LLM-`suggested_aliases` for one-click approval or rejection (05). Approval promotes that exact value to `aliases`; rejection removes the suggestion without creating a deny-list.
- **Monitoring history** (`/runs`) — run-centric list, not a flat cross-run response list: one row per run (date, trigger badge for `initial`/`manual`, status badge, `N of N responses`, visibility %, a "not yet analyzed" badge when terminal but unanalyzed), plus a "Next run" line from `next_run_at`. A running run renders a compact four-stage strip (Preparing → Asking ChatGPT → Analyzing → Done, derived from `monitoring_runs`/`prompt_results`/`result_analyses` counts — never Temporal history, which stays ops-only) inline in its row. Each desktop row has an explicit keyboard-accessible action that opens `/runs/:id`; the full stage strip plus that run's response table lives there (by prompt, mention, status; the run filter is dropped since the route already scopes it). Below `md`, both the run list and run-detail results reflow into cards with persistent open/filter controls and at least 44px touch targets; desktop retains semantic tables and neither mobile view introduces horizontal page overflow. Failed results show status + error; succeeded-but-unanalyzed show a "not yet analyzed" badge (05's soft-failure posture made visible instead of silently miscounted).
- **Settings** (`/setup`) — active-business profile editor with dirty/save feedback, repeatable structured controls for aliases and services, a question-management entry point, approved competitor-alias editing across every status, and read-only plan display. Each alias occupies its own control, so punctuation such as commas remains part of the value. Draft businesses redirect to onboarding; there is no regenerate action after activation (03). The client sends only changed profile fields. `UpdateBusiness` merges omitted fields against the current tenant-owned row for validation, treats an omitted `website` as unchanged and an explicit empty `website` as clear, then atomically updates only the supplied columns so a concurrent disjoint edit cannot restore stale values. Competitor alias edits are trim-normalized and case-insensitively deduplicated; they never mutate suggestions or history.
- **How we measure** — authenticated trust page explaining that OpenSight uses the OpenAI Responses API with web search and business-location context as a proxy, not a capture of chatgpt.com. API requests have no history, memory, or personalization and routing can differ; the API-reported model is stored per response. Visibility is mentions divided by analyzed valid responses, excluding failures and not-yet-analyzed responses. The page emphasizes traceability and that results are neither accuracy nor future-performance guarantees. It is linked from the Overview header, response-drawer model line, app footer, and Privacy.
- **Privacy** — authenticated plain-language summary of actual stored account, profile, prompt, raw response, derived analysis, and operational configuration data; the patient-data usage boundary; qualified OpenAI processing; verified-vs-planned hosting; and retention. It is linked from the app footer and How we measure.

## Frontend stack and structure

Vite + React + TypeScript. Deliberately small kit, consistent with solo maintenance:

- **TanStack Query**, via **connect-query** hooks generated from the proto schema, for all server state; no Redux/global store — server is the source of truth and the app is read-heavy. A single `createConnectTransport` instance (`web/src/api/transport.ts`), wrapped in `<TransportProvider>`, is shared app-wide — connect-query keys its cache by transport identity, so a second instance would silently split the cache.
- **react-router** with routes mirroring the five sections: `/overview`, `/prompts`, `/competitors`, `/runs` (+ `/runs/:id`), `/setup`, plus `/onboarding` for the draft flow. Root, successful login, and unknown authenticated routes land on `/overview`. `/responses(?run=X)` redirects to `/runs`/`/runs/:id` for old deep links.
- **Tailwind + shadcn/ui** for components, initialized from preset `bLTjNXma` (style `rhea` with Lucide icons). The app and marketing site share a warm paper/deep-ink foundation, Bricolage Grotesque display type, Instrument Sans body type, a pine-green primary brand color, and marker yellow for evidence highlights and positive change. The compact sight mark (green field, white aperture, yellow evidence point) identifies both surfaces. Semantic shadcn tokens remain the app styling API, and the fixed colorblind-safe chart-token palette stays independent from brand colors. Content uses a shared page-heading pattern inside an approximately 1180px shell. Charts use **shadcn's `Chart` component** (wraps Recharts) with no separately styled charting layer.

```
web/src/
├── api/            transport.ts (the shared Connect transport), errors.ts
│                   (ConnectError helpers), labels.ts (enum → display text),
│                   hooks.ts (the few hand-written hooks connect-query has no
│                   equivalent for)
├── components/     ResponseDrawer, TrendChart, StatCard, MentionBadge, …
├── gen/            generated Protobuf messages + connect-query method
│                   descriptors (opensight/v1/**) — committed, never hand-edited
├── pages/          overview/ prompts/ competitors/ runs/ setup/ onboarding/
└── lib/
```

## Degraded and empty states (specified now, because they're the first thing users see)

- **First run in progress** (post-onboarding): every section shows a progress state polling run status, not empty charts — this is the honest version of PRD success criterion 4.
- **Partial run**: banner on Overview ("18 of 20 prompts succeeded this week") linking to failed results; visibility math already excludes failures (04).
- **Single analyzed run**: the Visibility explorer defaults to Run snapshot. Over time is unavailable until a second analyzed run exists, so the UI never fabricates a future tick or suggests an observed trend where none exists.

## Open questions (owned by 07)

- Session mechanics, login UX, and whether MVP has any self-serve signup or is invite/manual.
- Rate limiting on the API (probably just a reverse-proxy limit at MVP).
