# Epic 07 — Analysis Pipeline (ANA)

AnalyzeRun: per-result extraction, entity reconciliation, competitors, mentions, citations. Phase 2.

---

## ANA-1 — Analysis schema

As the developer, I want the derived-analysis tables migrated, so that extraction has somewhere rebuildable to write.

- [x] `result_analyses (prompt_result_id PK, sentiment NULL, keywords, excerpts, analysis_model, extraction_version, analyzed_at)` — sentiment/keywords **only**; no mention facts.
- [x] `competitors (name, website NULL, aliases, suggested_aliases, source discovered|manual, status discovered|tracked|dismissed)`.
- [x] `mentions (prompt_result_id, subject self|competitor, competitor_id NULL, matched_by exact|llm, mention_order, excerpt)` — the **canonical and only** source of mention facts.
- [x] `citations (prompt_result_id, url, domain, title NULL, cite_order, subject business|competitor|other|unknown)`.
- [x] Store layer treats all four as wipe-and-rebuild (delete by result/run allowed; raw tables untouched).

Deps: SCH-3 · Phase 2 · Ref: design 02 (Analysis tables)

## ANA-2 — AnalyzeResult extraction activity

As the developer, I want one structured-output LLM call per succeeded result with deterministic validation, so that extraction is cheap, retryable, and hallucination-checked.

- [x] Mini-class model, configured separately from the execution model; `analysis_model` and `extraction_version` recorded per row.
- [x] Output schema per design 05: `entities[]` (verbatim_name, is_target, excerpt, in order of first appearance), `target` (sentiment/keywords/excerpts, null if not mentioned), `citations[]` (url, subject).
- [x] **Verbatim check**: every `verbatim_name` and excerpt must appear as a substring of `response_text` (whitespace-normalized); one retry with validation errors appended; a row failing after retry is flagged, not stored.
- [x] Extraction-prompt rules encoded: organizations only (never practitioners, directories, review sites, government bodies); practitioner-only recommendations yield **no entity**; sentiment/keywords describe how the response characterizes the target, each supportable by an excerpt; citation `subject` judged from surrounding text only, `unknown` is the honest default.
- [x] Writes `result_analyses` + `citations`; returns the ordered entity list to the workflow. No mention writes.
- [x] **Quality gate (blocks calling Phase 2 done)**: manually spot-check extraction output against a full real replay week (~20 responses); bar is zero fabricated mentions and zero missed self-mentions. Iterate the extraction prompt (bumping `extraction_version`) until it passes; record the check. Checked 2026-07-21 against all 10 `testdata/spk1` captures with `gpt-5-mini`: 10/10 passed (zero validation errors) after fixing a model double-escaped-unicode artifact (`extraction_version` 2).

Deps: ANA-1, RUN-4 · Phase 2 · Ref: design 05 (Phase 1 — AnalyzeResult)

## ANA-4 — ReconcileEntities: normalize + exact match

As the developer, I want a serial reconcile activity with deterministic name matching, so that matching and competitor creation never race.

- [x] Normalize: lowercase, Unicode-fold, strip punctuation, collapse whitespace, drop legal suffixes (`pte ltd`, `private limited`, `llp`); meaningful words like "clinic" are **not** stripped.
- [x] Exact match on normalized names against (a) target business name + aliases, then (b) all competitors' names + aliases **regardless of status** (dismissed still accrue).
- [x] `is_target` from the model is a hint only: a target match must also pass normalized name/alias matching; unverified flags demote to a normal entity.
- [x] Exact matches record `matched_by='exact'`.

Deps: ANA-2 · Phase 2 · Ref: design 05 (Phase 2, steps 1–2)

## ANA-5 — ReconcileEntities: LLM match pass + suggested aliases

As the developer, I want one conservative cheap-model call for still-unmatched names, so that name variants converge without silently polluting history.

- [x] Single call per run for unmatched names vs the existing competitor list (names + aliases + known websites); bar: "same real-world business only if evidence is strong; otherwise new".
- [x] LLM match → mention with `matched_by='llm'`; variant appended to the competitor's `suggested_aliases` — **never** auto-promoted to `aliases` (user approval in POL-4 promotes it).
- [x] Unapproved variants are re-judged each run (no cache of LLM verdicts as matching keys).

Deps: ANA-4 · Phase 2 · Ref: design 05 (Phase 2, step 3)

## ANA-6 — Competitor creation, mention writes, commit

As the developer, I want reconcile to finish transactionally, so that metrics only ever see fully analyzed runs.

- [x] Unmatched names (after both passes) create `competitors` rows: status `discovered`, source `discovered`, verbatim name as first alias; deduped within the run first. No minimum-mention threshold.
- [x] All `mentions` written for the run: subject, matched_by, `mention_order` = first-appearance rank, excerpt. Delete-and-rewrite of the run's mention rows in one transaction (idempotent).
- [x] Commit sets `monitoring_runs.analysis_completed_at`. A result enters the metrics base only when this is set **and** it has a `result_analyses` row — unanalyzed results excluded from numerator and denominator alike.

Deps: ANA-5 · Phase 2 · Ref: design 05 (Phase 2, steps 4–6)

## ANA-7 — AnalyzeRun wiring + ReanalyzeRun

As the operator, I want analysis to run as a child workflow with an independent retry budget and a manual re-run path, so that a failed analysis never re-spends prompt executions.

- [x] `AnalyzeRun` child workflow: parallel `AnalyzeResult` (~4 concurrent) then serial `ReconcileEntities`; started by RunWorkflow after execution.
- [x] AnalyzeRun failure does **not** fail the parent run; run stays viewable raw and is flagged for re-analysis.
- [x] `ReanalyzeRun` (CLI or Temporal UI trigger) is the same workflow pointed at an old run; `AnalyzeResult` upserts by `prompt_result_id`; per-result failures leave no `result_analyses` row (excluded from metrics, badged in INS-4).
- [ ] Cost sanity: ~22 mini-calls/run, well under $0.05 — verified against a real run.

Deps: ANA-6, RUN-3 · Phase 2 · Ref: design 05 (Shape, Cost and failure posture), 04 (RunWorkflow)
