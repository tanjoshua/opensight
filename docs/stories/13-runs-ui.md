# Epic 13 — Runs UI (RUNS)

Rename the Responses section to **Runs** and make it run-centric: a list of past/in-progress runs
with a "next run" line, drilling into `/runs/:id` for that run's responses. A running run shows a
four-stage progress strip (Preparing → Asking ChatGPT → Analyzing → Done) derived from
`monitoring_runs`/`prompt_results`/`result_analyses` counts on the existing poll — not from
Temporal workflow history, which stays an ops-only surface (design 04). Phase 3, post-MVP.

---

## RUNS-1 — Persist expected result count

As the developer, I want `monitoring_runs` to record how many prompts a run expects, so that
progress ("k of N") is derived from a stored fact instead of the current active prompt count.

- [x] `monitoring_runs.expected_results int` (nullable — old rows stay null, no backfill).
- [x] `LoadRunSpec` writes it (already computes `len(prompts)`); `FinalizeRun` reads it from the
      row instead of taking it as a workflow-passed argument.
- [x] Deriving N from current active prompts is wrong for historical runs after a prompt
      replacement — this column is why.

Deps: — · Phase 3 · Ref: design 04 (RunWorkflow, LoadRunSpec/FinalizeRun)

## RUNS-2 — Run aggregates and next-run time on ListRuns

As a clinic user, I want each run's progress counts and the next scheduled run's date visible via
the API, so that the frontend can render status without a second round trip.

- [x] `Run` proto gains `expected_results`, `succeeded_results`, `failed_results`,
      `analyzed_results` (int32); `ListRuns` computes them via aggregate join, no new query per run.
- [x] `ListRunsResponse` gains `google.protobuf.Timestamp next_run_at`, sourced from
      `ScheduleClient().GetHandle(ScheduleID(...)).Describe()` → `Info.NextActionTimes[0]`. (Shipped
      without the `optional` keyword — message fields already have proto3 presence, and the rest of
      `result.proto` doesn't mark its Timestamp fields optional either; the nullable/best-effort
      behavior is unchanged.)
- [x] Schedule lookup is best-effort: a Temporal error is logged and leaves `next_run_at` unset,
      never fails the request.
- [x] No synthetic `Run` row for the next run — a fake row would leak into trend/visibility math.
- [x] No `GetRun` endpoint — the detail page selects its run out of the already-cached, already-
      polling `ListRuns` result (~52 runs/year; one query, one cache key, one poll).

Deps: RUNS-1 · Phase 3 · Ref: design 06 (ResultService), design 04 (Schedule)

## RUNS-3 — Run progress derivation and stage strip

As a clinic user, I want to see which stage a running run is at, so a mid-run visit reads as
progress instead of an unexplained wait.

- [x] `web/src/api/run-progress.ts`: single pure function mapping a `Run` to
      `{ stage, label, counts, stages }` — Preparing (0 results) / Asking ChatGPT (`k of N`, failures
      counted separately) / Analyzing (`analyzed of succeeded`) / Done (terminal status badge).
      One derivation shared by Overview, the Runs list, and run detail so they can't disagree (not
      the shell badge — `app-layout.tsx`'s `RunProgressBadge` stays a static "Run in progress"
      label with no stage derivation).
- [x] `RunStageStrip` component, full (detail page) and compact (list row) variants, shadcn
      primitives only — vertical/horizontal stage rows with state icon + label, no new charting or
      DAG dependency (the pipeline is linear with one fan-out; a graph would overstate it).
- [x] Partial/failed run: strip surfaces the warning at the Asking stage ("18 of 20 succeeded")
      linking to the failed rows. Analysis still in flight (`analysis_completed_at` unset) reads as
      a neutral in-progress state ("Still analyzing", spinner icon), not a warning: FinalizeRun runs
      before AnalyzeRun (design 04), so every healthy run sits terminal-but-unanalyzed for a normal,
      often multi-minute span, and there is no field distinguishing that from an actually-stuck
      analysis — so no warning/error treatment is invented for a case the data can't support.
- [x] `workflow_id` surfaced in a collapsed "Technical details" block on run detail only — no
      Temporal Web UI embed or link for end users (design 04: that surface stays ops-only).

Deps: RUNS-2 · Phase 3 · Ref: design 04 (run stages, failure posture)

## RUNS-4 — Runs list page

As a clinic user, I want to browse runs (not raw responses) as the primary view, so that I see the
weekly cadence of monitoring rather than a flat, unscoped result list.

- [x] Route `/runs` replaces `/responses`; row per run: date + trigger badge (First run / Manual;
      nothing for scheduled) · status badge · `N of N responses` · visibility % (`ListRuns` already
      returns it) · "not yet analyzed" badge when terminal but unanalyzed.
- [x] Top-of-list "Next run" line from `next_run_at`, date only (no timezone derivation — the
      schedule fires at a fixed UTC hour and the business profile carries no timezone).
- [x] A running run renders the compact `RunStageStrip` inline in its row.
- [x] The current flat cross-run response list is dropped, not kept as a tab — per-prompt history
      already lives on the Prompts page; the only internal consumers are run-scoped deep links
      (RUNS-6).

Deps: RUNS-3 · Phase 3 · Ref: design 06 (Responses section note, superseded by this doc)

## RUNS-5 — Run detail page

As a clinic user, I want to open a run and see its responses, so drilling from the list to the
evidence stays one click.

- [x] Route `/runs/:id`: full `RunStageStrip` at top, then today's response table (prompt/status
      filters kept, run filter dropped since the route scopes it) filtered to that run.
- [x] Response rows open the existing shared `ResponseDrawer` — unaffected by this epic's changes.
- [x] Run selected from the already-fetched `ListRuns` cache (RUNS-2) — no per-run fetch.

Deps: RUNS-4 · Phase 3 · Ref: design 06 (the response drawer contract)

## RUNS-6 — Nav rename and deep-link migration

As a clinic user, I want every existing entry point that used to land on Responses to keep working,
so the rename doesn't break saved links or in-app navigation.

- [x] Nav label "Responses" → "Runs"; `/responses` and `/responses?run=X` redirect to `/runs` and
      `/runs/:id`.
- [x] Update the three internal deep links (Overview trend-week click, partial-run banner,
      competitor trend click) to `/runs/:id`. (Also updated four more call sites the story text
      didn't name but the rename requires: the two post-login landing redirects and the app's
      index/wildcard redirects — all previously pointed at `/responses`.)
- [x] Overview's "First run in progress" state renders `RunStageStrip` instead of its current bare
      message.
- [x] Design docs 02, 04, and 06 updated in place: the `expected_results` column, route list,
      Responses section note renamed/rewritten as Runs, run-progress source of truth and the
      FinalizeRun-before-AnalyzeRun ordering documented.

Deps: RUNS-5 · Phase 3 · Ref: design 06 (route list, section notes)

---

## Deferred (explicitly out of this epic)

- "Run now" manual trigger — spends money per click; needs an entitlement/rate-limit decision first.
- Re-analyze a past run from the UI — stays a Temporal CLI action (design 05).
- Per-prompt in-flight/retry visibility — requires Temporal workflow history, ops-grade information.
- Any Temporal Web UI embed or user-facing link.
- SSE/websocket live updates — existing poll-while-running is sufficient at this run volume.
- Schedule editing (day/time/frequency) — plan-driven, not user-editable at Starter.
- Per-run duration/cost surfaced to the user — cost lives in `raw_response`, stays an ops query.
- "Your weekly results are ready" email — post-MVP alerts.
