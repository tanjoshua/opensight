# Epic 08 — Metrics API (MET)

One shared metrics package and the endpoints behind Overview, Prompts, and Competitors. Phase 2.

---

## MET-1 — internal/metrics package

As the developer, I want all metrics computed in one shared package, so that sections can never disagree on what visibility means.

- [x] `internal/metrics`: visibility % (self-mentions ÷ analyzed results in run), weekly trend by `scheduled_for`, mention order, sentiment/keyword aggregates, citation frequency by domain, competitor stats (mention %, totals, avg order, per-prompt appearances, trend, vs-self).
- [x] Mention facts read **only** from `mentions`; a result counts only when its run has `analysis_completed_at` **and** it has a `result_analyses` row.
- [x] Every aggregate carries the `result_ids` behind it (the "every number is a door" contract).
- [x] Unit tests against seeded replay data pin the gating rules (partial runs, unanalyzed results excluded from both sides).

Deps: ANA-6 · Phase 2 · Ref: design 06 (API conventions), 02 (metrics mapping)

## MET-2 — Overview endpoint

As a clinic user, I want a single overview payload, so that the headline page loads in one request.

- [x] `GET /businesses/:id/overview`: current visibility %, delta vs previous run, top keywords, top cited domains, top competitors (tracked + top-3 discovered by coverage), latest run status.
- [x] Includes prompt-set-change dates (from prompt created/retired) for trend markers.
- [x] All aggregates carry `result_ids`.

Deps: MET-1 · Phase 2 · Ref: design 06 (Overview), PRD §7

## MET-3 — Prompts endpoints

As a clinic user, I want per-prompt performance and full history, so that I can see which prompts surface my business.

- [x] `GET /businesses/:id/prompts`: active prompts with latest-result summary (mentioned?, order, sentiment) + spark-trend series.
- [x] `GET /prompts/:id`: full result history and lineage links (`replaces_prompt_id` chain), including retired prompts.
- [x] Latest analyzed result per prompt drives presence/absence.

Deps: MET-1 · Phase 2 · Ref: design 06 (Prompts), 02 (Prompts)

## MET-4 — Competitors endpoint

As a clinic user, I want competitor stats compared to my own, so that I can see who ChatGPT recommends instead of me.

- [x] `GET /businesses/:id/competitors` with `?status` filter: each with mention %, total mentions, avg mention order, per-prompt appearances, weekly trend, vs-self comparison; discovered ranked by response coverage.
- [x] Dismissed competitors keep their history (display filter only).
- [x] Paginated; aggregates carry `result_ids`.

Deps: MET-1 · Phase 2 · Ref: design 06 (Competitors), PRD §6

## MET-5 — Result detail enrichment

As a clinic user, I want the response detail to include its analysis, so that the drawer can show evidence, not just raw text.

- [x] `GET /results/:id` now includes: mentions (with subject, verbatim_name, order, matched_by, excerpts), sentiment + supporting excerpts, keywords, citations with subjects and annotation spans for inline markers.
- [x] `GET /businesses/:id/results` gains the `mentioned` filter; runs endpoint gains per-run visibility %.
- [x] Succeeded-but-unanalyzed results flagged in the payload (drives the UI badge).

Deps: MET-1 · Phase 2 · Ref: design 06 (Responses, Runs endpoints)

## MET-6 — Citation sources drill-down

As a clinic user, I want to drill from a cited domain into its pages and the prompts that cite it, so that I know which sources to get listed on (the PRD's per-source promise, fully delivered).

- [x] `GET /businesses/:id/citations` (or equivalent): per domain — citation frequency, cited pages (url + title), associated prompts, per-source business/competitor/other subject split; aggregates carry `result_ids`.
- [x] Overview's top-cited-domains panel rows (INS-1) open this drill-down; rows within it open the Response drawer.
- [x] ~1 day: the aggregation already exists in MET-1; this story is the endpoint + a drill-down view.

Deps: MET-1, INS-1 · Phase 2 · Ref: PRD §6 (Citation Sources), design 06 (Overview)
