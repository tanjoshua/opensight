# Design 09 — Visibility Assessments and Opportunities

Depends on: [02 Data model](02-data-model.md), [04 Monitoring](04-monitoring.md), [05 Analysis](05-analysis.md), [06 API + frontend](06-api-frontend.md)

## Product model

The pipeline is:

`practice catalog → evidence collectors → assessments → opportunity compiler → outcome evaluators`

A visibility practice is the durable product concept. An opportunity is an optional action derived only from an active `PARTIAL` or `NOT_MET` assessment. Opportunities provide a small prioritized queue; persisted assessments provide the complete coverage model for a future checklist. The checklist API and page are deferred.

The compiled Go catalog gives every practice a stable key, criteria version, section, user-facing explanation, evidence tier, references, applicability, and optional assessor/presenter/evaluator keys. A practice without an automatic assessor remains `UNKNOWN`. Criteria versions change when meaning or pass criteria change; assessor module versions change for implementation-only updates.

## Module contracts and safety

Collectors emit compact, versioned JSON artifacts validated by collector-owned types:

- `monitoring-snapshot`: confirmed business identity plus the latest four analyzed runs, prompt/result references, mentions, competitors, and citations.
- `owned-site-scan`: checked URLs, bounded text summaries, hashes, access/indexing signals, and verification time.

Assessors declare owned practices, required collectors, module version, rollout mode, presenter/evaluator keys, and hard budgets for runtime, research and output count. Targeted URL inspection is available only through the bounded researcher, which reuses the onboarding fetcher's SSRF-safe HTTP client and accepts only HTTP(S) URLs. Unknown evidence versions and ownership mismatches fail closed.

Presentation is limited to standard typed blocks: text, metric with result IDs, validated link, question list, evidence list, and notice. Modules cannot return HTML or Markdown. The compiler rejects invalid blocks and only compiles active unmet assessments. Product copy must not promise rankings, fabricate reviews, encourage spam, make unsupported claims, or denigrate competitors. Regulated businesses must review claims before publishing.

## Initial practices

### `discoverability.openai_search_access`

Subject: the business website host. `MET` requires reachable, indexable public pages without an `OAI-SearchBot` robots denial. Confirmed robots denial, authentication, `noindex`, or unusable response is `NOT_MET`; transient inspection failure is `UNKNOWN`. `NOT_MET` is an active direct blocker and ranks first.

This practice deliberately excludes schema, sitemap, page-length and generic SEO checks.

### `authority.influential_source_presence`

Subject: each influential source domain. The source must appear in at least two affected questions within four analyzed runs, or recur for one question across two runs. Inspected presence is `MET`; verified competitor presence with business absence is `NOT_MET`; unreliable inspection is `UNKNOWN`. It deploys in `SHADOW` until fixture quality, cost, safety and manual review gates pass.

### `owned_site.tracked_topic_coverage`

Subject: a deterministic service/location/customer-need topic derived from tracked questions. Clear accessible coverage is `MET`, incomplete coverage is `PARTIAL`, no clear coverage is `NOT_MET`, and unreliable access is `UNKNOWN`. An actionable result requires business absence from at least two current related responses or recurrence across runs. It deploys in `SHADOW` under the same promotion gates.

## Persistence and identity

- `assessment_generations` is unique per monitoring run and records status, compiler/ranker versions, the resolved module plan and timestamps.
- `evidence_artifacts` records collector provenance, payload version, checked time, status and compact payload.
- `visibility_assessments` records practice/assessor provenance, subject, five-state result, rollout mode, evidence references, ranking features and versioned payload. Successful `MET` and shadow results are retained.
- `opportunities` has durable `(business_id, practice_key, subject_key)` identity. New assessments refresh evidence and presentation without replacing user status.
- `opportunity_events` is append-only and idempotent for lifecycle and outcome observations.

All deep reads and writes are account scoped. A criteria-version change makes the prior assessment historical; the new practice is unknown until evaluated. Failed generations preserve the previous successful opportunity set.

## Temporal orchestration

After `AnalyzeRun` succeeds, `RunWorkflow` starts one idempotent `AssessmentWorkflow` child. A Temporal version marker preserves replay compatibility with histories created before this stage existed.

1. `ResolveAssessmentPlan` validates deployment modes against the compiled registry, persists the generation, and records the sorted module plan in workflow history.
2. Each required collector runs once; collector failures do not suppress independent modules.
3. Generic `RunPracticeAssessor` activities run when their dependencies succeeded and persist active and shadow results.
4. The compiler refreshes existing opportunity evidence, ranks active unmet assessments, and stores at most five opportunities.
5. Completed opportunities receive idempotent later outcome observations from their evaluator.
6. The generation finishes `READY`, `PARTIAL`, or `FAILED`.

Workflow code sorts collector/module keys and performs no I/O. Network, database, current-time and registry/config resolution stay in activities.

## Ranking and lifecycle

Ranking is versioned independently and orders by direct prerequisite blocker, affected-question reach, persistence, evidence quality, actionability, lower effort, then stable practice and subject keys. The API exposes list, detail and status mutation. The UI shows at most three non-dismissed focus items and keeps completed/dismissed history available.

States are `OPEN`, `IN_PROGRESS`, `COMPLETED`, and `DISMISSED`. Dismissal requires `NOT_RELEVANT`, `ALREADY_DONE`, `NOT_ACTIONABLE`, `TOO_MUCH_EFFORT`, or `OTHER`. Completion freezes result/prompt IDs and time. A later assessment may verify `MET`, but the UI reports it as an observation rather than causal proof.

Deployment configuration uses `VISIBILITY_ASSESSOR_MODES` with explicit `assessor=DISABLED|SHADOW|ACTIVE` entries. Defaults are active search access and shadow authority/topic modules. Runtime plugins, database-authored rules and prompt-only modules are out of scope.

## Deferred checklist

The future checklist groups current assessments by catalog section and shows status, confidence, checked date, criteria version, evidence limits, related opportunity and verification history. It says “All currently assessed practices are met,” never “You have done everything.” Unknown, experimental and unassessed practices remain visible.
