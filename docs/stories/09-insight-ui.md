# Epic 09 — Insight UI (INS)

Overview, Prompts, and Competitors sections; the drawer becomes evidence-grade. Phase 2.

---

## INS-1 — Overview page

As a clinic user, I want one page answering "how visible am I and what changed", so that weekly check-ins take a minute.

- [x] Brief header states the exact latest analyzed run date and denominator; the visibility hero shows mentioned/analyzed counts, an explicit complete-evidence action, dated delta when a baseline exists, and the weekly trend (shadcn `Chart`). Clicking a trend point deep-links to that run.
- [x] "What changed" compares only the two latest analyzed points for the same active prompt (newly visible, no longer visible, still absent); one-point/new prompts are labeled as lacking a baseline.
- [x] "Where to focus" surfaces latest absent questions, cited domains (rows open the citation sources drill-down, MET-6), and leading competitors (tracked + top-3 discovered, with "N discovered → triage") as evidence to review. Common themes are secondary.
- [x] Partial-run banner ("18 of 20 prompts succeeded this week") linking to failed results.
- [x] Single data point still renders inside the trend chart itself (dot + axes, x-axis extended one interval to a "Next run" tick) so it reads as day one of a growing trend, not a placeholder; every stat opens the Response drawer via its `result_ids`.

Deps: MET-2, WEB-4 · Phase 2 · Ref: design 06 (Overview, Degraded states), PRD §7

## INS-2 — Prompts page

As a clinic user, I want the 20 prompts with their latest results and trends, so that I can see where I appear and where I'm absent.

- [x] Table of active prompts: mentioned?, mention order, sentiment, sparkline across runs.
- [x] Row click → prompt detail with full history; retired prompts reachable via lineage ("replaced X on date").
- [x] Presence/absence and all numbers open the Response drawer.

Deps: MET-3, WEB-4 · Phase 2 · Ref: design 06 (Prompts), PRD §6 Visibility

## INS-3 — Competitors page (read + compare)

As a clinic user, I want discovered and tracked competitors with comparisons against me, so that I know who wins the prompts I lose.

- [x] Discovered list ranked by response coverage ("in 7 of 20 responses"); tracked list with PRD comparison stats vs self; dismissed collapsed but recoverable.
- [x] Weekly trend per tracked competitor; all stats open the Response drawer.
- [x] (Track/dismiss/add/alias-approval actions are POL-3/POL-4 — this story is read-only comparison.)

Deps: MET-4, WEB-4 · Phase 2 · Ref: design 06 (Competitors), PRD §6

## INS-4 — Response drawer v2

As a clinic user, I want the response drawer to show highlighted evidence, so that every claim traces to the text.

- [x] Answer text with self/competitor mentions highlighted and **inline citation markers at their annotation spans**.
- [x] Sentiment + supporting excerpts, keyword chips, citation list with subjects; raw JSON toggle retained.
- [x] Succeeded-but-unanalyzed results show a "not yet analyzed" badge (here and in the Responses list).

Deps: MET-5, WEB-4 · Phase 2 · Ref: design 06 (The one UI contract, Responses)
