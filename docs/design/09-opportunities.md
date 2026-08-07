# Design 09 — Visibility Assessments and Opportunities

Depends on: [02 Data model](02-data-model.md), [04 Monitoring](04-monitoring.md), [05 Analysis](05-analysis.md), [06 API + frontend](06-api-frontend.md)

## Product model

The pipeline is:

`practice catalog → evidence collectors → assessments → opportunity compiler → outcome evaluators`

A visibility practice is the durable product concept. An opportunity is an optional action derived only from a `PARTIAL` or `NOT_MET` assessment. Opportunities provide a small prioritized queue; persisted assessments provide the complete coverage model for a future checklist. The checklist API and page are deferred.

A catalog entry is the complete description of a practice: stable key, criteria version, section, title, user-facing explanation and rationale, evidence tier, references, suggested steps, whether the practice is a direct prerequisite blocker, whether its subject belongs in the presented title, and its assessor key. Ranking and presentation read those fields, so neither names a practice and adding a practice is one catalog entry plus one assessor. Evidence tier is catalog metadata and is not surfaced in the product. A practice without a registered assessor remains `UNKNOWN`. Criteria versions change when meaning or pass criteria change; assessor module versions change for implementation-only updates.

## Module contracts and safety

Collectors emit compact, versioned JSON artifacts validated by collector-owned types:

- `monitoring-snapshot`: confirmed business identity plus the latest four analyzed runs, prompt/result references, mentions, competitors, and citations.
- `owned-site-scan`: checked URLs, bounded text summaries, hashes, access/indexing signals, and verification time.

Assessors declare owned practices, required collectors, module version, and hard budgets for runtime, research and output count. Targeted URL inspection is available only through the bounded researcher, which reuses the onboarding fetcher's SSRF-safe HTTP client and accepts only HTTP(S) URLs. Unknown evidence versions and ownership mismatches fail closed.

Presentation is limited to standard typed blocks: text, metric with result IDs, validated link, question list, evidence list, and notice. Modules cannot return HTML or Markdown. The compiler rejects invalid blocks and only compiles unmet assessments. One catalog-driven presenter renders every practice, and one evaluator observes the latest assessment status of every completed opportunity; the `Presenter` interface remains the seam for a practice that needs bespoke rendering, and `OpportunityEvaluator` is the seam the outcome evaluator specified below plugs into. Product copy must not promise rankings, fabricate reviews, encourage spam, make unsupported claims, or denigrate competitors. Regulated businesses must review claims before publishing.

## Initial practices

### `discoverability.openai_search_access`

Subject: the business website host. `MET` requires reachable, indexable public pages without an `OAI-SearchBot` robots denial. Confirmed robots denial, authentication, `noindex`, or unusable response is `NOT_MET`; transient inspection failure is `UNKNOWN`. It is the only practice the catalog marks as a direct blocker, so `NOT_MET` ranks first.

The collector reads every signal from the pages the site fetcher already retrieved — access status and indexing directives included — so the scan asks the site for nothing beyond the pages themselves and `robots.txt`. Robots rules are evaluated against the paths of those retrieved pages as well as the site root, so a rule that denies the prefix the content lives under is a denial even when the root is allowed. A missing `robots.txt` (4xx) disallows nothing, while an unreadable one (5xx or transport failure) is an inspection failure and yields `UNKNOWN` unless another barrier is already confirmed. The homepage is judged on its own: authentication or a `noindex` directive there is a barrier for the site even when every other page is public, because it is the most linked and most cited page and a refusal there is the signature of bot protection blocking OpenAI. Away from the homepage a single `noindex` page is incidental and only a directive on every retrieved page is a barrier. Between a false `MET` and a false `NOT_MET` on the only shipping check, the verdict errs toward `NOT_MET`.

This practice deliberately excludes schema, sitemap, page-length and generic SEO checks.

### `authority.influential_source_presence`

Subject: each influential source domain. The source must appear in at least two affected questions within four analyzed runs, or recur for one question across two runs. Inspected presence is `MET`; verified competitor presence with business absence is `NOT_MET`; unreliable inspection is `UNKNOWN`. Its assessor is implemented but unregistered until it clears the promotion gate below, so the practice stays unassessed.

### `owned_site.tracked_topic_coverage`

Subject: a deterministic service/location/customer-need topic derived from tracked questions. Clear accessible coverage is `MET`, incomplete coverage is `PARTIAL`, no clear coverage is `NOT_MET`, and unreliable access is `UNKNOWN`. An actionable result requires business absence from at least two current related responses or recurrence across runs. Its assessor is likewise implemented but unregistered under the same promotion gate.

### Promotion gate

Whether an assessor is good enough to register is a number, not a judgement call. Every implemented assessor is scored against a hand-labelled fixture corpus in `internal/visibility/testdata`: 15–20 stored collector payloads each, every case carrying the correct `AssessmentStatus` for one practice and subject plus the reason that answer is right, and `NONE` where the correct behaviour is to produce no assessment at all. The corpora deliberately over-weight the cases the heuristics are known to get wrong, so a score is a stress score rather than an expected field accuracy. Fixtures never reach the network: research-backed assessors are scored against a stub researcher that serves only the pages a fixture declares and enforces the manifest's URL budget.

**An assessor is registered once it answers 90% of its corpus correctly.** Registration and enforcement are one switch: a registered assessor's wrong answer fails the build, an unregistered one's is reported without failing, because the score is the artifact and a red build would only invite softening the labels. Read the current scores with `go test -v ./internal/visibility/ -run Quality`. Unregistered assessors keep their calibrated manifests — the output and inspection budgets are load-bearing, so losing them along with registration would silently make the assessor return nothing.

## Persistence and identity

- `assessment_generations` is unique per monitoring run and records status, compiler/ranker versions, the resolved module plan and timestamps.
- `evidence_artifacts` records collector provenance, payload version, checked time, status and compact payload. Stored evidence is replayable: `opensight assess replay` re-derives verdicts from it with today's assessors and reports what a change to them would alter. Replay writes nothing — `assessment_generations` is unique per monitoring run, so a replay cannot record itself as a second generation, and writing into the existing one would destroy the historical verdict a criteria change is meant to preserve. An assessor that no longer understands a stored `payload_version` fails that generation visibly rather than being skipped.
- `visibility_assessments` records practice/assessor provenance, subject, five-state result, evidence references, ranking features and versioned payload. Successful `MET` results are retained.
- `opportunities` has durable `(business_id, practice_key, subject_key)` identity and splits in two. The user owns status, dismissal reason, completion baseline and first-seen time. The compiler owns rank, presentation and the current assessment/generation pointers, and rebuilds that half wholesale each generation inside one transaction: it upserts the compiled items against the running generation, then clears `current_generation_id` on every other row of the business. A non-null `current_generation_id` therefore means "still detected by the newest generation", and a practice that turns `MET` simply stops being current instead of being rewritten. A row that stops being current keeps its last-known presentation so acted-on history still renders; one the user never acted on stops being listed.
- `opportunity_events` is append-only and idempotent for lifecycle and outcome observations.

All deep reads and writes are account scoped. A criteria-version change makes the prior assessment historical; the new practice is unknown until evaluated. Failed generations preserve the previous successful opportunity set.

## Temporal orchestration

After `AnalyzeRun` succeeds, `RunWorkflow` starts one idempotent `AssessmentWorkflow` child. A Temporal version marker preserves replay compatibility with histories created before this stage existed.

1. `ResolveAssessmentPlan` validates the registered assessors against the compiled catalog, persists the generation, and records the sorted module plan in workflow history.
2. Each required collector runs once; collector failures do not suppress independent modules.
3. Generic `RunPracticeAssessor` activities run when their dependencies succeeded and persist their results.
4. The compiler ranks unmet assessments and rebuilds the opportunity projection from at most five of them.
5. Completed opportunities receive idempotent later outcome observations from their evaluator, matched to this generation's assessment by practice identity so an item verified `MET` after completion is still observed.
6. The generation finishes `READY`, `PARTIAL`, or `FAILED`.

Workflow code sorts collector/module keys and performs no I/O. Network, database, current-time and registry/config resolution stay in activities.

## Ranking and lifecycle

Ranking is versioned independently and orders by direct prerequisite blocker — the catalog flag, not a named practice — then affected-question reach, persistence, evidence quality, actionability, lower effort, and finally stable practice and subject keys. The API exposes list, detail and status mutation, and carries the blocker flag on each opportunity so the UI's "Fix first" badge needs no knowledge of practice keys.

Focus has one definition — current, `OPEN` or `IN_PROGRESS`, and among the top three by rank — computed once in SQL and returned unchanged by every read. The UI places each returned opportunity in exactly one section: focus, more opportunities (current but outside the focus three), no longer detected, completed, or dismissed. Nothing the API returns can be invisible.

States are `OPEN`, `IN_PROGRESS`, `COMPLETED`, and `DISMISSED`. Dismissal requires `NOT_RELEVANT`, `ALREADY_DONE`, `NOT_ACTIONABLE`, `TOO_MUCH_EFFORT`, or `OTHER`. Completion freezes the baseline the outcome evaluator measures against. A later assessment may verify `MET` or stop detecting the practice, but the UI reports either as an observation rather than causal proof.

Which assessors run is a compiled-in decision, not deployment configuration: the registry in code is the single source of truth, and a practice whose assessor is not registered is simply never assessed. Runtime plugins, database-authored rules and prompt-only modules are out of scope.

## Deferred stages

Outcome observation, generation health and the practice checklist are specified and not yet built. The columns, interfaces and stored records each one needs already exist; nothing reads them yet.

### Outcome observation

The evaluator answers one question — on the questions this opportunity affected, has anything changed since the user marked it complete — by comparing a frozen baseline against a later analyzed run and recording the difference as a dated reading. The practice's current assessment status is a re-check rather than a measurement and accompanies the reading instead of standing in for it.

**Baseline.** Completion freezes a self-contained record rather than a pointer into data that scrolls out of the monitoring window: the completion time, and for each affected question the prompt, its latest analyzed result, and whether that result mentioned the business. The affected set is the prompts behind the assessment's result IDs together with any prompt IDs it carried. An assessment that carries neither, as a site-level prerequisite does, freezes the mentioned and analyzed counts of the latest analyzed run instead and is measured against the whole tracked question set, with that denominator named in every reading so the figure is visibly account-wide. Which of the two applies is read off the baseline, so no code names a practice.

**Timing.** Readings are taken at the second and the fourth analyzed run scheduled after the completion time, and never again. The first run is a settling run: a site or listing change is not necessarily re-crawled and re-answered within one weekly cycle, so a reading there reports the absence of a change that had no chance to appear. The second run is the earliest reading worth showing; the fourth is a month of weekly monitoring later and separates a persistent change from a single-run flip, matching the four-run window the monitoring snapshot carries. Only analyzed runs advance the count, on the same gated base as every other metric. The thresholds are "at least two" and "at least four" rather than exact equality, so a generation that never ran delays a reading instead of losing it, and each reading records the count it was taken at. Two observations per completed opportunity is the entire retention story: the bound is structural, keyed `outcome:run-2` and `outcome:run-4` against the append-only event table's idempotency, and there is nothing to prune. The baseline is frozen once, so restoring and re-completing an item does not restart the window.

**Comparison.** Both sides are computed over the frozen questions that are still tracked and analyzed in the reading run. A frozen question that was retired or replaced is dropped from the baseline as well as from the reading, and a replacement prompt is never substituted for its predecessor because a replacement starts a new trend. Recomputing both sides over the surviving set is what stops a shrinking denominator from manufacturing a delta; the reading names how many frozen questions it dropped. When none survive, the reading records that the question set no longer supports a comparison and carries no numbers.

**Inputs.** Current per-question mention state comes from this generation's `monitoring-snapshot` artifact through the `EvidenceView` the evaluator already receives. The frozen baseline and the number of analyzed runs since completion arrive on the `Opportunity`, that count computed in the same query that selects completed candidates. The evaluator performs no I/O and holds no clock.

**What the user sees.** A completed opportunity lists its readings, each dated, each linking to the responses behind every number, each next to the practice's current assessment status. A reading states the count mentioned now, the frozen count, the named question set, and both dates: "18 March: you appear in 3 of the 5 questions this item affected, which showed 0 mentions when you marked it complete on 4 March." Unchanged and negative readings are stated in the same shape and are never omitted. A reading whose question set no longer supports a comparison says so.

**Language.** Every outcome sentence is a dated reading of monitored data with both endpoints named, and attributes the reading to nothing.

Permitted: counts and rates over the named question set, both dates, the direction of the difference, "no change", "not comparable", and links to the underlying responses.

Never stated or implied: that the completed work caused, improved, drove, lifted or resulted in the change; that a change will persist or continue; any ranking, guarantee, score or grade; any aggregate framed as the value of completed work. "After" is permitted as calendar ordering only, never as "after you fixed…". The prohibition covers implication as much as wording — a reading appears as an entry in a dated observation list, never as a before/after result panel headed by the completed action, and the product publishes no per-account "opportunities completed → visibility gained" summary. Every reading carries the standing qualifier that it is an observation and not proof of cause. Readings describe the user's own monitoring data and are not publishable performance claims; the requirement that a regulated business review claims before publishing is unchanged.

### Generation health

`assessment_generations.status` and `evidence_artifacts.status` record every failure, and the failure modes they cover are quiet: a collector that starts timing out, or an assessor whose `UNKNOWN` rate climbs because a heuristic drifted, degrades assessment quality with no external symptom. Three readings over a window make them visible:

- **Generation outcomes** — count and share per status. A nonzero `FAILED` count is actionable on sight; a rising `PARTIAL` share means a collector or assessor is failing without stopping the run.
- **Collector failures** — per collector key, failed over total, with the most recent error text. A collector that begins timing out surfaces here first.
- **Assessor status distribution** — per assessor key, counts per assessment status. The `UNKNOWN` share is the early-warning signal for quality decay, because a drifted heuristic stops answering before it starts answering wrongly; a sustained rise against the previous window is the trigger to re-score that assessor against its fixture corpus and unregister it if it no longer clears the promotion gate.

Each is one `GROUP BY`. They are exposed as `opensight assess health`, read-only, with the same `--business`, `--since` and `--until` filters as `assess replay`, and join the operator's manual weekly check (07) rather than a dashboard. There are no thresholds, alerts or stored aggregates: the tables are the artifact and the operator reads them.

### Checklist

The future checklist groups current assessments by catalog section and shows status, checked date, criteria version, evidence limits, related opportunity and verification history. It says “All currently assessed practices are met,” never “You have done everything.” Unknown, experimental and unassessed practices remain visible.
