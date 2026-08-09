# Design 05 — Analysis Pipeline

Depends on: [02 Data Model](02-data-model.md) (`result_analyses`, `mentions`, `citations`, `competitors`), [04 Monitoring](04-monitoring.md) (`AnalyzeRun` child workflow)

## Shape

`AnalyzeRun` is the child workflow started by `RunWorkflow` after prompt execution. It runs in two phases — parallel extraction, then a single serial reconcile — because entity matching and competitor creation must not race across concurrent activities:

```
AnalyzeRun(runID)
 ├─ AnalyzeResult ×N   parallel (~4), one per succeeded prompt_result
 │                     LLM extraction → writes result_analyses + citations,
 │                     returns ordered entity list (verbatim names)
 └─ ReconcileEntities  single activity, serial:
                       match names → competitors, create 'discovered' rows,
                       write all mentions for the run
```

Analysis is **derived and rebuildable** (02): both phases overwrite their own outputs idempotently — `AnalyzeResult` upserts by `prompt_result_id`; `ReconcileEntities` deletes-and-rewrites the run's mention rows in one transaction. Failed analysis never touches the raw results, and `ReanalyzeRun` (manual trigger, or after an extraction-prompt improvement) is the same workflow pointed at an old run. There is no separate `ReanalyzeRun` workflow function: `AnalyzeRun` takes `{AccountID, RunID}`, so re-analysis is just `AnalyzeRun` started directly from the Temporal CLI/UI (`temporal workflow start --type AnalyzeRun --workflow-id analyze-<run-id> --input '{"AccountID":"…","RunID":"…"}'`).

`RunWorkflow` runs `FinalizeRun` **before** starting the `AnalyzeRun` child: `monitoring_runs`' CHECK forbids stamping `analysis_completed_at` unless `completed_at` is already set, and the failure posture is that a run reaches its terminal status independently of analysis — analysis merely decorates it, or fails and leaves it flagged for re-analysis.

## Phase 1 — AnalyzeResult (per response)

One structured-output LLM call per succeeded result. A small/cheap model (mini-class), configured separately from the execution model; `analysis_model` is recorded per row. One call per result rather than batching the run: retry granularity, bounded context, and at ~20 calls/run the cost is pennies.

**Inputs**: `response_text`, the raw citation annotations, the target business's name + aliases + category + location, and the prompt text.

**Output schema** (validated in Go; one retry with validation errors appended). Validation includes a **verbatim check**: every `verbatim_name` and every excerpt must appear as a substring of `response_text` (whitespace-normalized). This is a free, deterministic hallucination detector on exactly the fields users read as evidence — a row that fails after retry is flagged rather than stored. `result_analyses.extraction_version` records the extraction-prompt version so future improvements can target "re-analyze everything below version N".

```jsonc
{
  "entities": [           // every ORGANIZATION recommended or discussed,
    {                     // in order of first appearance
      "verbatim_name": "…",
      "is_target": false, // model's judgment using the supplied aliases
      "excerpt": "…"      // the sentence where it first appears
    }
  ],
  "target": {             // null if business not mentioned
    "sentiment": "positive|neutral|negative|mixed",
    "keywords": ["…"],    // descriptors/themes applied to the business
    "excerpts": ["…"]     // quotes supporting sentiment + keywords
  },
  "citations": [          // aligned to the response's citation annotations
    {
      "cite_order": 0,
      "url": "…",
      "subject": "business|competitor|other|unknown",
      "entity_indices": [0, 2]
    }
  ]
}
```

Rules encoded in the extraction prompt:

- Entities are **organizations only** (clinics, practices, hospitals) — never individual practitioners or employees, and not directories, review sites, or government bodies (those appear as citation domains instead). A response that recommends only a person ("see Dr Tan Wei Ming") without naming an organization yields **no entity** for that recommendation: practitioner-only mentions are not business mentions and never become competitors. Matching keys are organization trading names exclusively. Extraction test fixtures (replay data, 07) must cover the three canonical cases: practitioner-only, organization-only, and combined.
- Sentiment and keywords describe **how the response characterizes the target business**, not the response's overall tone. Every keyword and the sentiment must be supportable by an excerpt — excerpts are the user-facing evidence (PRD §6) and our spot-check surface against extraction hallucination.
- Citation `subject` is judged from the response's own text around the citation, never by fetching the cited page (02 decision). `unknown` is the honest default.
- Every citation annotation is returned as a separate occurrence in `cite_order`, including repeated URLs. `entity_indices` contains every organization directly supported by that occurrence, multiple indexes when appropriate, and an empty array for general guidance or an unclear relationship.

**Citation evidence and entity links.** A `url_citation` annotation's `start_index`/`end_index` cover the inline marker rather than the supported claim. Annotation spans are therefore retained only to display the surrounding inline evidence: after sorting annotations by `start_index`, each marker stores `[max(previous marker end, start of its line), marker start)` as `text_start`/`text_end`. A URL check with a marker-location fallback keeps these display offsets robust when provider indexes differ.

Entity attribution comes directly from the same extraction call's validated `entity_indices`; there is no positional fallback. Validation requires exact citation occurrence coverage, `cite_order` and URL agreement, and unique in-range entity indexes in addition to the verbatim entity evidence. Invalid output retries once through the normal extraction retry path. An uncertain relationship produces no link.

Writes: `result_analyses` (sentiment, keywords, excerpts) and `citations` — **no mention facts**; those come exclusively from phase 2 into `mentions` (02). If reconcile later demotes the model's `is_target` judgment, the stored sentiment simply never surfaces, since metrics gate on `mentions`. Returns the entity list with its model-supplied citation orders to the workflow for phase 2.

## Phase 2 — ReconcileEntities (per run)

Serial, so name-matching and competitor creation have no races and one dedupe pass covers the whole run.

1. **Normalize** every verbatim name: lowercase, Unicode-fold, strip punctuation, collapse whitespace, drop legal suffixes (`pte ltd`, `private limited`, `llp`). Meaningful words like "clinic" are *not* stripped — "Atlas Clinic" and "Atlas Orthopaedics" are different businesses.
2. **Match (exact pass)** against (a) the target business's name + aliases, then (b) all existing competitors' names + aliases, *regardless of status* — mentions of dismissed competitors still accrue (02: dismissal is a display filter). Exact-on-normalized only; no fuzzy string distance. The model's `is_target` flag is a hint, but a target match must also pass normalized alias matching — an unverified flag demotes to a normal entity (conservative: better to surface a false "competitor" the user can merge than silently inflate own visibility).
3. **Match (LLM pass)** — one cheap-model call for the run's still-unmatched names, judged against the existing competitor list (names + aliases + any known websites). Bar is deliberately conservative: *"same real-world business, only if the evidence is strong; otherwise new"* — because a wrong split is visible and fixable, while a wrong merge silently pollutes a competitor's trend. On a match: write the mention with `matched_by='llm'` and record the variant in the competitor's `suggested_aliases`; the reconcile write boundary trims surrounding whitespace and skips a now-empty value, so the stored value is the exact review key. **An alias becomes a permanent matching key only when the user approves it** (one click in the competitor detail, 06), which atomically removes that exact suggestion, appends it once to `aliases`, and hands future matching to the exact pass. Rejecting removes only the current suggestion; there is deliberately no deny-list, so later evidence may suggest the same variant again. Until approved, variants are re-judged by the LLM pass each run. Exact-pass matches record `matched_by='exact'`.
4. **Create** a `competitors` row (status `discovered`, source `discovered`, the verbatim name as first alias) for names unmatched by both passes, deduping within the run first. A newly-discovered competitor's own triggering mention records `matched_by='exact'` — it exact-matches the competitor's just-minted alias, so no new `mentions.matched_by` enum value is needed.
5. **Write** `mentions` for every entity occurrence, then write every supplied link to `mention_citations` by `(prompt_result_id, cite_order)`, in the same transaction. An empty link set means no citation clearly supported that organization, so a reader can never mistake “cited somewhere in this answer” for “cited for this business.”
6. **Commit** — set `monitoring_runs.analysis_completed_at`. A result enters the metrics base only when this is set *and* it has a `result_analyses` row: succeeded-but-unanalyzed results are excluded from numerator and denominator alike, so an analysis failure can never masquerade as a visibility drop — it shows as a badge instead (06).

No minimum-mention threshold for discovery: every recommended provider becomes a `discovered` row — suppressing at creation throws away unrecoverable data; suppressing at display costs nothing. Noise control is a display concern (06): the Competitors tab shows everything ranked by response coverage, while Overview auto-surfaces only tracked competitors plus the top few discovered. Expected volume with 20 same-category prompts: ~15–40 unique competitors after run one, growing slowly.

Known limitation, accepted: despite the LLM pass, some real-world businesses will still end up split across two competitor rows. The onboarding research step (03) seeds aliases to reduce this for the target business; a "merge competitors" admin action remains future work — noted, not built.

## Cost and failure posture

- ~22 mini-model calls per run (20 extractions + 1 reconcile matching call + retry slack): well under $0.05/run — negligible next to execution (04).
- Per-result extraction failure after retries → that result simply has no `result_analyses` row and is excluded from visibility math entirely (commit step above); the UI badges it. `ReanalyzeRun` picks up stragglers.
- `AnalyzeRun` failure does not fail the parent run (04): raw responses are already viewable.

## Where the extraction prompt lives

Extraction and competitor matching use strict structured outputs through the official OpenAI Go SDK. Their prompts state only semantic rules the schemas cannot encode: organization-only extraction, exact evidence quotes, citation-subject evidence, and conservative identity matching.

The extraction prompt text, its JSON schema, and its version live together in code (`internal/llm`), not in config. `ExtractionPromptVersion` is a Go `int` constant co-located with the prompt; a prompt or schema change and its version bump are one commit. Each analyzed row records that version as `result_analyses.extraction_version`, so a later pass can target "re-analyze everything below version N" after an extraction-prompt improvement.

## Open questions (owned by later increments)

- **06**: how discovered competitors are presented for track/dismiss triage; unanalyzed-result display.
- **07**: spend alerting shared with execution.
