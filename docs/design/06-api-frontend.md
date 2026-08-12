# Design 06 — API and Frontend Structure

Depends on: [02 Data Model](02-data-model.md), [03 Onboarding](03-onboarding.md) (onboarding endpoints), [04 Monitoring](04-monitoring.md) (run status), [05 Analysis](05-analysis.md) (display filtering)

## API conventions

- Protobuf schema under `proto/opensight/v1/` (package `opensight.v1`), served over **Connect RPC** at `/rpc` by the Go binary; the SPA is static files from the same binary, so no CORS in production (Vite dev server proxies `/rpc` locally).
- `make proto` (buf format + lint + generate) regenerates code from the `.proto` files: Go structs + Connect handler interfaces into `internal/gen/opensight/v1/`, and TS messages + connect-query method descriptors into `web/src/gen/opensight/v1/`. Both trees are committed — CI re-runs `make proto` and fails on any diff, so generated code can never drift from the schema.
- Auth: sign-in itself is not an RPC — `GET /auth/google/start` and `GET /auth/google/callback` (07 "Auth and accounts") are plain HTTP routes mounted outside `/rpc`, alongside `/healthz` and `/webhooks/stripe`. A Connect interceptor resolves the session cookie to a global user. Account-scoped requests also carry `X-OpenSight-Account-Slug`, derived from `/a/:accountSlug/...`; the interceptor resolves that user's membership, role, account, plan, and billing access. Unknown and unauthorized slugs both return `NotFound`. One exhaustive, default-deny procedure policy declares identity/account scope, minimum membership role, and billing access class, so an unclassified RPC fails tests rather than silently gaining access. **Every account handler receives account context and every repository call is account-scoped** — there is no unscoped query path.
- A persistent banner in the app shell (`web/src/components/billing-banner.tsx`, BILL-10) renders whenever `useBillingAccess()` reports `lapsed`: generic and non-dated (the exact dates stay one click away on `/billing`), stating that monitoring has stopped and linking to reactivation.
- MVP has one business per account, but requests carry `business_id` anyway. The API validates business→account ownership on every request; account membership currently grants access to all of its businesses.
- Errors: a `connect.Error` from a small fixed set of codes (`Unauthenticated`, `NotFound`, `FailedPrecondition`, `ResourceExhausted`, `AlreadyExists`, `InvalidArgument`, `Internal`) carrying a client-safe message — the real error always goes to `slog`, never the client. Pagination: `limit`/`offset` request fields plus a `Paging` response message, only where lists can grow (results, competitors, citations).
- All metrics are computed server-side in one shared `internal/metrics` package (SQL over 02's tables) — Overview, Prompts, and Competitors must never disagree because they computed visibility differently. Mention facts come only from `mentions`, and visibility counts only analyzed results (02/05): succeeded-but-unanalyzed results are excluded from the math and badged.

## Services and RPCs

Onboarding RPCs are already defined in 03. The product services cover the five product sections (PRD §7), identity, account selection, team access, billing, and onboarding:

```
# AuthService
# Sign-in is GET /auth/google/start → /auth/google/callback, not an RPC (07)
Logout()
GetMe()                                     → global user and account-membership summaries

# AccountService
GetAccountContext(account_slug)             → account, caller role, businesses, access, plan
CreateAccount(name)                         → unpaid Starter account + owner membership
ListMembers(account_slug)                   → active and pending members
AddMember(account_slug, email, role)         → immediate membership; no email or acceptance step
UpdateMemberRole(account_slug, user_id, role)
RemoveMember(account_slug, user_id)

# BusinessService (onboarding + Setup)
CreateBusiness(name, website)               → draft business summary; starts onboarding
GetBusiness(business_id)                    → full profile (plan entitlements moved to GetMe, BILL-6 — not
                                              a per-business fact)
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
  mentioned?, limit, offset)                  caller order after business/account scoping
GetResult(result_id, include_raw)           → full record: response text, analysis, mentions,
                                              citations, run/model metadata, raw JSON on demand

# ImproveService
ListActions(business_id)                    → three focus actions, additional active actions,
                                              latest check time and typed empty reason
GetAction(action_id)                        → action detail, steps, source and response evidence
SetActionStatus(action_id, status, reason)  → complete, dismiss, or reopen
GetChecklist(business_id)                   → ordered groups, all site checks, outcome counts,
                                              check time, pages read and crawl failure
```

## Schema design rules

- `AccountRole` has `OWNER`, `ADMIN`, `MEMBER`, and `VIEWER` values (plus the required unspecified zero value). Roles belong to `AccountMembership`, never `User`: one identity may own one account and only view another.
- `viewer` may read product and team data; `member` may edit normal business, prompt, and competitor data; `admin` may also manage non-owner members and trigger spend; `owner` alone manages billing and ownership. Admins cannot grant, demote, or remove owners. Owners may manage all roles, but the API rejects removal or demotion of the last owner with `FailedPrecondition`.
- Insufficient role returns `PermissionDenied`; billing-state denial remains `FailedPrecondition`. Role changes and removal take effect on the next request without destroying the user's global session.
- Instants (`started_at`, `completed_at`, …) are `google.protobuf.Timestamp`; date-only values (`scheduled_for`) stay plain `YYYY-MM-DD` strings — a calendar date isn't an instant and doesn't need timezone-aware wire semantics.
- Every closed domain set is a real proto enum with a `*_UNSPECIFIED = 0` zero value (e.g. `BusinessStatus`, `RunStatus`, `Sentiment`); fields backed by an open or non-domain string stay a plain `string` (`Run.platform`, `Plan.run_interval`) rather than a false enum.
- PATCH-style partial updates use `optional` scalar fields (proto3 presence: omitted = unchanged) plus a `StringList` wrapper message (`common.proto`) for repeated fields, since a bare `repeated string` cannot distinguish "omitted, leave unchanged" from "present but empty, clear" from "present with values, replace". `UpdateBusinessRequest` is the canonical example: `optional string website` — omitted leaves it unchanged, present-and-empty clears it; `StringList aliases` — nil leaves it unchanged, present-with-zero-values clears it, present-with-values replaces it.
- Opaque JSON payloads (`PromptResult.request_json`, `raw_response_json`) are plain `string`, not `google.protobuf.Struct` — `Struct` encodes every number as `double`, which would silently mangle the int64 token counts in stored OpenAI response payloads.
- **Every number is a door**: every aggregate/summary message carries `repeated string result_ids` under that exact field name — the analyzed-result ids backing that number, so the frontend can always drill from a stat to its evidence (see below).

## The one UI contract: every number is a door

PRD §6's "every metric links to the underlying response" is implemented as a single pattern, not per-feature plumbing: **every aggregate the API returns carries the `result_ids` behind it**, and every stat component in the UI is clickable → opens that number's evidence. Charts deep-link the same way: clicking a week on a competitor trend line opens that run's detail page, and the Brief's trend links out to Monitoring history.

A door leads to one of two surfaces, chosen by what the reader came to do:

- The **Response drawer** — a slide-over rendering `ResultService.GetResult`, used from the Brief, Competitors, cited sources, and Improve. These are sections where a stat is inspected *in passing* and returned from, so the evidence must not cost the reader their place. It carries the rendered answer, sentiment + excerpts, mentions, the citation list, model + timestamp, raw JSON behind a toggle, and a link onward to the full question page.
- The **question page** (`/prompts/:id`) — the reading view, used from Questions, where reading the answer *is* the task rather than a detour. Stats and spark-trend dots on the Questions table lead here, the dots at their own run.

Both render the answer through one shared component: the model's markdown, with business/competitor mentions highlighted and **citations rendered at their annotation spans**. Because `url_citation` annotations are character offsets into the raw markdown, the renderer parses to mdast and keeps `position.offset` on every node, intersecting the source-offset ranges with each text node's own window — a markdown renderer that discards source positions would land the highlights on the wrong characters. A citation that appears in the prose as a link becomes its numbered chip in place (and the parenthesis left holding nothing but chips is dropped with them); one with no link of its own gets a superscript at the end of its span.

Shared `SheetContent` widths must be expressed unprefixed on the active side. A default written as `data-[side=right]:sm:max-w-sm` carries a variant a caller's `sm:max-w-2xl` does not, so `tailwind-merge` keeps both and the default silently wins — which is how two drawers spent their lives at 384px.

## Section notes

- **Brief** (`/overview`) — the authenticated landing page and a concise visibility briefing. Its page header describes the briefing without repeating run metrics. One adaptive **Visibility explorer** card has **Run snapshot** and **Over time** modes; changing its run or range affects only this card. Question changes and absence elsewhere use each question's latest analyzed responses, while cited sources, common themes, and competitors summarize evidence collected to date. The explorer presents one prominent, run-aware visibility percentage with its exact date; in Snapshot it follows the selected run, while Over time shows the latest visible result. Mode controls sit beside this headline, range controls sit above the trend, and Snapshot puts its evidence and run actions in a separated footer. With one analyzed run it defaults to Snapshot and does not render a fabricated future axis. With two runs it defaults to a straight before→after trend. With three or more it defaults to a straight-segment line over the last 12 runs; when history permits, Last 4, Last 12, and All ranges are available. Snapshot can select any analyzed run and states its exact date. It shows horizontal comparison bars for the business plus up to three current Overview competitors that have data for that run, falling back to a mentioned/not-mentioned composition bar when none do — the same snapshot Monitoring history renders per run. Every comparison value exposes its per-run `result_ids`. Over time plots exact observed values with no curve interpolation or gap-connecting and may include at most two current competitor lines. It answers direction only: points carry a hover/focus tooltip with each series' value and the denominator, but no point selection, no per-run detail panel, and no dated control row — a single run is inspected in Monitoring history, which the trend links to in its caption. Prompt-set-change markers appear only after the first visible observed run and through the last visible run; a native keyboard-accessible annotation disclosure lists their dates and descriptions. Only Over time shows the **What changed** card, which compares the two latest analyzed points belonging to the same active prompt and shows actual newly-visible and no-longer-visible transitions; Snapshot proceeds directly to evidence because its selected-run delta already supplies the relevant comparison. Still-absent questions remain in the evidence area, and prompts with fewer than two points have no baseline and are not classified. A balanced four-card **Explore the evidence** grid presents latest absent questions, competitors, cited domains, and common themes as evidence to inspect, not recommendations. Its ranked lists are capped at five items; links lead to the full Questions and Competitors sections. The competitor panel shows tracked competitors plus top-3 discovered by coverage (the 05 display filter), with a discovered-review link into Competitors. The header always links to **How we measure**, including before any analyzed data exists.
- **Questions** (`/prompts`) — semantic desktop table of monitored prompts with per-prompt: mentioned?, order, sentiment, and sparkline across runs. Below the `md` breakpoint it reflows into cards instead of horizontally scrolling: the question, latest metrics, response history, and a single action into the question remain visible with touch targets at least 44px high. Every door on this table — the row, each stat, each spark dot — leads to the question page, the dots carrying `?run=` for their own run. **Replace flow (PRD §4)**: modal states exactly what happens — "history for the old prompt stays viewable; the new prompt starts a fresh trend" — and the API requires `confirmed: true`, so the warning is structurally unskippable.
- **Question** (`/prompts/:id`) — the reading view for one question, and the only place a full answer is read. The question itself is the page title, not a card under a "Prompt" label. Under it, a **run strip** of dated pills replaces what was a result-history table: one pill per run oldest-first, each carrying its own outcome as a mark — filled (mentioned), hollow (analyzed, absent), dashed (not yet analyzed), and a destructive diamond (failed) — so the scanning affordance of the table's sparkline survives on this page. The strip scrolls horizontally and keeps the selected pill in view. Selection lives in `?run=<run_id>` and is set with `replace`, so an answer is linkable without a year of runs burying the Questions list in history; an unrecognised or absent `run` falls back to the newest.
  Below the strip, the answer sits in a **capped reading column** (~32rem, about 80 characters at the answer's own type size — note that `ch` units resolve against the *wrapper's* font size and badly overestimate this) beside a **sticky analysis rail**. The rail indexes the same evidence the answer is carrying: the self-mention verdict and its order, sentiment, the excerpts behind that verdict, competitors in mention order, the numbered sources, and themes. Rail and answer are linked in both directions — selecting a source or a competitor scrolls its inline chip or highlight into view and flashes it once. Only mentions actually located in the text offer that action. Below both, full width (JSON is data to inspect, not prose to read), a **Technical details** disclosure holds status, model with its How-we-measure link, timestamps, run context, the request, and raw JSON on demand.
  A failed run shows its error in place of an answer; a succeeded-but-unanalyzed one shows the answer with an explicit note that it carries no highlights or evidence yet (05's soft-failure posture). A question with no results yet says so rather than rendering an empty strip. Retired prompts remain reachable from a prompt's lineage ("replaced X on date"), and `GetPrompt` reaches them, so a retired predecessor renders here normally.
- **Competitors** — triage-first: a compact discovered list is ranked by response coverage ("in 7 of 20 responses") with one-click track/dismiss. Tracked competitors use a comparison table on desktop and summary cards on small screens; selecting one opens a single detail panel with the PRD comparison stats, prompt appearances, approved and suggested aliases, evidence set, and a larger weekly trend that includes zero-mention analyzed weeks. Dated native controls below the trend provide keyboard-accessible navigation to each run. A manual add starts tracked and remains selectable with zero metrics and an explicit empty-history state until it is mentioned; its trend still carries zero points for analyzed runs. Dismissed stays collapsed but recoverable (data was never deleted, per 02). Track, dismiss, and restore actions give local success feedback. Undo is offered only for the true inverse transitions between tracked and dismissed; triaging a discovered competitor cannot offer Undo because the status API deliberately cannot restore `discovered`. Every competitor status exposes pending LLM-`suggested_aliases` for one-click approval or rejection (05). Approval promotes that exact value to `aliases`; rejection removes the suggestion without creating a deny-list.
- **Monitoring history** (`/runs`) — run-centric list, not a flat cross-run response list: one row per run (date, trigger badge for `initial`/`manual`, status badge, `N of N responses`, visibility %, a "not yet analyzed" badge when terminal but unanalyzed), plus a "Next run" line from `next_run_at`. A running run renders a compact four-stage strip (Preparing → Asking ChatGPT → Analyzing → Done, derived from `monitoring_runs`/`prompt_results`/`result_analyses` counts — never Temporal history, which stays ops-only) inline in its row. Each desktop row has an explicit keyboard-accessible action that opens `/runs/:id`. That page leads with the run's **Visibility snapshot** — the Brief's snapshot chart with its mentioned-of-analyzed denominator, read from the already-cached `GetOverview` trend point for that run and omitted when the run has no analyzed point — followed by the full stage strip and that run's response table (by prompt, mention, status; the run filter is dropped since the route already scopes it). Below `md`, both the run list and run-detail results reflow into cards with persistent open/filter controls and at least 44px touch targets; desktop retains semantic tables and neither mobile view introduces horizontal page overflow. Failed results show status + error; succeeded-but-unanalyzed show a "not yet analyzed" badge (05's soft-failure posture made visible instead of silently miscounted).
- **Improve** — `/improve/actions` shows three focus findings followed by all additional active findings, with direct steps, sources, supporting responses, and an empty state that never claims health. Completed and dismissed findings remain reachable in one collapsed section for confirmation and explicit reopening, without an event timeline. `/improve/checklist` renders the same 17 site checks on every visit in Access, Structure, and Identity groups; plain-language assertions and outcomes stay scannable while each row expands to explain the check and its exact evidence. It calculates no score. `/improve` navigates to actions; there is no activity or legacy opportunities route.
- **Settings** (`/a/:accountSlug/setup`) — active-business profile editor with dirty/save feedback, repeatable structured controls for aliases and services, a question-management entry point, approved competitor-alias editing across every status, and read-only plan display. Each alias occupies its own control, so punctuation such as commas remains part of the value. Draft businesses redirect to onboarding; there is no regenerate action after activation (03). The client sends only changed profile fields. `UpdateBusiness` merges omitted fields against the current account-owned row for validation, treats an omitted `website` as unchanged and an explicit empty `website` as clear, then atomically updates only the supplied columns so a concurrent disjoint edit cannot restore stale values. Competitor alias edits are trim-normalized and case-insensitively deduplicated; they never mutate suggestions or history.
- **Members** (`/a/:accountSlug/team`) — the UI calls the account boundary a **workspace**: a quiet collaboration and billing container around its businesses. Members lives in the workspace section of navigation, separate from business monitoring and profile settings. It lists active and pending members and exposes role-appropriate add, role-change, and remove controls. Adding by email grants access immediately in the database and shows pending until the email is linked by Google sign-in; OpenSight sends no invitation email.
- **How we measure** — authenticated trust page explaining that OpenSight uses the OpenAI Responses API with web search and business-location context as a proxy, not a capture of chatgpt.com. API requests have no history, memory, or personalization and routing can differ; the API-reported model is stored per response. Visibility is mentions divided by analyzed valid responses, excluding failures and not-yet-analyzed responses. The page emphasizes traceability and that results are neither accuracy nor future-performance guarantees. It is linked from the Overview header, response-drawer model line, app footer, and Privacy.
- **Privacy** — authenticated plain-language summary of actual stored account, profile, prompt, raw response, derived analysis, and operational configuration data; the patient-data usage boundary; qualified OpenAI processing; verified-vs-planned hosting; and retention. It is linked from the app footer and How we measure.

## Frontend stack and structure

Vite + React + TypeScript. Deliberately small kit, consistent with solo maintenance:

- **TanStack Query**, via **connect-query** hooks generated from the proto schema, for all server state; no Redux/global store — server is the source of truth and the app is read-heavy. A single `createConnectTransport` instance (`web/src/api/transport.ts`), wrapped in `<TransportProvider>`, is shared app-wide — connect-query keys its cache by transport identity, so a second instance would silently split the cache.
- **react-router** with `/accounts` for workspace selection and `/accounts/new` for the intentionally undiscoverable workspace-creation flow. Every product route remains internally account-prefixed: `/a/:accountSlug/overview`, `/prompts`, `/competitors`, `/runs` (+ `/runs/:id`), `/improve/actions`, `/improve/checklist`, `/setup`, `/team`, `/billing`, and `/onboarding`. The UI presents the monitored business as the everyday context and reserves **workspace** for the account's members, billing, and business collection. Billing, Members, How we measure, and Privacy share the authenticated sidebar shell even before payment or onboarding; the shell hides business navigation until an active business exists, while its route guard redirects unavailable monitoring pages to Billing or Members. A user with one membership forwards automatically and sees no workspace switcher; multiple memberships land on the workspace picker and get a sidebar switcher. Picker rows show human names, never implementation slugs. Account-scoped query keys include the slug and switching clears or namespaces account data so one account's data is never shown under another. Old `/responses(?run=X)` links redirect within the selected account.
- **Tailwind + shadcn/ui** for components, initialized from preset `bLTjNXma` (style `rhea`, stone base + chart colors and Lucide icons). The app and marketing site share one light, monochrome warm-paper/deep-ink foundation, Bricolage Grotesque display type, and Instrument Sans body type; the product does not follow the system color scheme or offer a separate dark appearance. Evidence emphasis uses quiet stone surfaces or fine ink underlines rather than a separate brand color. A minimal, single-color magnifying glass identifies both surfaces. Semantic shadcn tokens remain the app styling API, and the fixed colorblind-safe chart-token palette stays independent from brand colors. Content uses a shared page-heading pattern inside an approximately 1180px shell. Charts use **shadcn's `Chart` component** (wraps Recharts) with no separately styled charting layer.

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
