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

# ResultService (Responses + Runs, feeds all trend charts)
ListRuns(business_id)                       → run list: scheduled_for, status, visibility %
ListResults(business_id, run_id?,           → filters: run, prompt, mentioned, status; paginated
  prompt_id?, status?, mentioned?,
  limit, offset)
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

PRD §6's "every metric links to the underlying response" is implemented as a single pattern, not per-feature plumbing: **every aggregate the API returns carries the `result_ids` behind it**, and every stat component in the UI is clickable → opens the **Response drawer** (a slide-over rendering `ResultService.GetResult`: answer text with business/competitor mentions highlighted and **inline citation markers rendered at their annotation spans**, sentiment + excerpts, the citation list, model + timestamp, raw JSON behind a toggle). Charts deep-link the same way: clicking a week on a trend line opens Responses filtered to that run. One drawer component, used everywhere, is the whole traceability story.

## Section notes

- **Overview** — headline visibility stat + weekly trend line, then three compact panels (themes, cited domains, competitors). The trend line plots the business's own visibility ("You", dominant) alongside its top few competitors as secondary lines in the same visibility-percent terms, so relative standing is legible at a glance; each competitor in `top_competitors` therefore carries its weekly `trend` (the same per-run mention-% series the Competitors section computes — MET-4), and the chart caps competitor lines to the top few with a legend. Each line — "You" plus up to 3 competitors — gets its own color from a fixed-order, colorblind-safe categorical palette (`--chart-1`…`--chart-4`); "You" stays visually dominant via line/dot weight rather than color alone, and a legend still names every line since not all slots clear contrast on a light card. Competitor panel shows tracked competitors plus top-3 discovered by coverage (the 05 display filter), with a "N discovered → triage" link into Competitors. The weekly trend renders **prompt-set-change markers** (derived from prompt created/retired dates) so a prompt change never reads as a visibility change. Its header always links to **How we measure**, including before any analyzed data exists.
- **Prompts** — table of 20 with per-prompt: mentioned? order? sentiment, sparkline across runs. **Replace flow (PRD §4)**: modal states exactly what happens — "history for the old prompt stays viewable; the new prompt starts a fresh trend" — and the API requires `confirmed: true`, so the warning is structurally unskippable. Retired prompts remain reachable from a prompt's lineage ("replaced X on date").
- **Competitors** — triage-first: discovered list ranked by response coverage ("in 7 of 20 responses") with one-click track/dismiss; tracked list with the PRD comparison stats vs self, prompt appearances, and weekly trend lines that include zero-mention analyzed weeks. A manual add starts tracked and remains visible with zero metrics and an empty prompt history until it is mentioned; its trend still carries zero points for analyzed runs. Dismissed collapsed but recoverable (data was never deleted, per 02). Every competitor status exposes pending LLM-`suggested_aliases` for one-click approval or rejection (05). Approval promotes that exact value to `aliases`; rejection removes the suggestion without creating a deny-list.
- **Responses** — filterable list (by run, prompt, mention, status). Failed results show status + error; succeeded-but-unanalyzed show a "not yet analyzed" badge (05's soft-failure posture made visible instead of silently miscounted).
- **Setup** — active-business profile editor with dirty/save feedback, repeatable structured controls for aliases and services, a prompt-management entry point, approved competitor-alias editing across every status, and read-only plan display. Each alias occupies its own control, so punctuation such as commas remains part of the value. Draft businesses redirect to onboarding; there is no regenerate action after activation (03). The client sends only changed profile fields. `UpdateBusiness` merges omitted fields against the current tenant-owned row for validation, treats an omitted `website` as unchanged and an explicit empty `website` as clear, then atomically updates only the supplied columns so a concurrent disjoint edit cannot restore stale values. Competitor alias edits are trim-normalized and case-insensitively deduplicated; they never mutate suggestions or history.
- **How we measure** — authenticated trust page explaining that OpenSight uses the OpenAI Responses API with web search and business-location context as a proxy, not a capture of chatgpt.com. API requests have no history, memory, or personalization and routing can differ; the API-reported model is stored per response. Visibility is mentions divided by analyzed valid responses, excluding failures and not-yet-analyzed responses. The page emphasizes traceability and that results are neither accuracy nor future-performance guarantees. It is linked from the Overview header, response-drawer model line, app footer, and Privacy.
- **Privacy** — authenticated plain-language summary of actual stored account, profile, prompt, raw response, derived analysis, and operational configuration data; the patient-data usage boundary; qualified OpenAI processing; verified-vs-planned hosting; and retention. It is linked from the app footer and How we measure.

## Frontend stack and structure

Vite + React + TypeScript. Deliberately small kit, consistent with solo maintenance:

- **TanStack Query**, via **connect-query** hooks generated from the proto schema, for all server state; no Redux/global store — server is the source of truth and the app is read-heavy. A single `createConnectTransport` instance (`web/src/api/transport.ts`), wrapped in `<TransportProvider>`, is shared app-wide — connect-query keys its cache by transport identity, so a second instance would silently split the cache.
- **react-router** with routes mirroring the five sections: `/overview`, `/prompts`, `/competitors`, `/responses`, `/setup`, plus `/onboarding` for the draft flow.
- **Tailwind + shadcn/ui** for components, initialized with preset `bLTjNXma` (style `rhea`, stone base + chart colors, Lucide icons, Roboto): `npx shadcn@latest init --preset bLTjNXma --template vite`. Charts use **shadcn's `Chart` component** (wraps Recharts, themed by the preset's chart-color variables) — no separately styled charting layer.

```
web/src/
├── api/            transport.ts (the shared Connect transport), errors.ts
│                   (ConnectError helpers), labels.ts (enum → display text),
│                   hooks.ts (the few hand-written hooks connect-query has no
│                   equivalent for)
├── components/     ResponseDrawer, TrendChart, StatCard, MentionBadge, …
├── gen/            generated Protobuf messages + connect-query method
│                   descriptors (opensight/v1/**) — committed, never hand-edited
├── pages/          overview/ prompts/ competitors/ responses/ setup/ onboarding/
└── lib/
```

## Degraded and empty states (specified now, because they're the first thing users see)

- **First run in progress** (post-onboarding): every section shows a progress state polling run status, not empty charts — this is the honest version of PRD success criterion 4.
- **Partial run**: banner on Overview ("18 of 20 prompts succeeded this week") linking to failed results; visibility math already excludes failures (04).
- **Single data point**: the Overview trend still renders inside the same chart frame (axes, grid, one dot) rather than swapping to a different layout — the x-axis stretches one interval past the point to a "Next run" tick (no fabricated date), so day-one clinics see the chart they'll grow into. Most users' second session is week one.

## Open questions (owned by 07)

- Session mechanics, login UX, and whether MVP has any self-serve signup or is invite/manual.
- Rate limiting on the API (probably just a reverse-proxy limit at MVP).
