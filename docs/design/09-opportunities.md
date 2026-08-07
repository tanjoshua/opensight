# Design 09 — Improve, visibility assessments, and actions

Depends on: [02 Data model](02-data-model.md), [04 Monitoring](04-monitoring.md), [05 Analysis](05-analysis.md), [06 API + frontend](06-api-frontend.md)

## Product model

The durable concept is a catalog practice. Each practice declares a stable key, ordered section, criteria version, mode (`CHECK`, `CONTINUOUS`, or `TRACKED`), subject scope (`BUSINESS` or dynamic), evidence tier, assessor, explanation, references, action steps, and whether it may recommend an action. Business-scoped practices always use subject key `business`; changing or omitting the website does not change identity.

Assessments describe what OpenSight knows. Improvement actions are repeatable work cycles derived from actionable assessments. Action state never changes an assessment standing, and action history never claims that work caused a later visibility change.

Checklist standings are `GOOD`, `IMPROVABLE`, `NEEDS_ATTENTION`, `TRACKING`, `COULD_NOT_VERIFY`, `NOT_ASSESSED`, `NOT_APPLICABLE`, and `NO_LONGER_TRACKED`. Checks present `GOOD` as “In place,” continuous practices as “In good standing” or “Room to improve,” and tracked practices as “Being tracked.” A tracked practice produces no action unless its catalog entry explicitly opts in.

## Evidence and assessors

Collectors emit compact versioned artifacts. `monitoring-snapshot` contains the latest four analyzed runs and their prompts, results, mentions, competitors, and citations. `owned-site-scan` contains the inspected host, checked URLs, extracted text, access/indexing signals, and failure detail. A missing website is a successful artifact describing an unverifiable check, so search access produces `COULD_NOT_VERIFY` rather than an invalid or absent assessment.

Assessors declare their owned practices, dependencies, module version, and hard runtime/research/output budgets. URL inspection uses the SSRF-safe bounded researcher. Unknown payload versions, ownership mismatches, and invalid drafts fail closed. Each planned assessor records `SUCCEEDED`, `FAILED`, or `SKIPPED` for every generation. The latest successful run of a dynamic assessor defines its current subject set.

Search access is a business-scoped `CHECK` and the only registered assessor. Influential-source presence and tracked-topic coverage are dynamic `CONTINUOUS` practices whose implementations remain unregistered until they clear the 90% hand-labelled fixture gate. Catalog entries without registered assessors still appear as `NOT_ASSESSED` in the checklist.

## Atomic publication and persistence

`assessment_generations` and `evidence_artifacts` retain provenance. `assessment_module_outcomes` records every planned assessor outcome. `visibility_assessments` retains every generation result and marks only the currently published result for each practice subject.

Publication is one transaction:

1. Record module outcomes.
2. Replace published assessments only for successfully evaluated practice scopes.
3. Retire missing dynamic subjects only when their assessor succeeded.
4. Reconcile actions only for those successful scopes.
5. Mark the generation `READY` or `PARTIAL`.

Failed or skipped scopes retain their prior published standings and actions and are reported stale. Compilation failure marks the generation `FAILED` without changing published state. Reads consider only `READY` and `PARTIAL` generations, never `RUNNING` or `FAILED`.

`improvement_actions` stores immutable numbered cycles with a stable `recommendation_key`. The active `OPEN` or `IN_PROGRESS` cycle is updated while the recommendation remains materially the same. Completion and dismissal are history: completion freezes a future-compatible baseline, and dismissal suppresses the same recommendation key. A completed practice recurs only after a verified good-to-actionable regression or a materially different recommendation key; a dismissed one recurs only when that key changes. Automatic retirement and supersession are append-only activity events. `improvement_action_events` records creation, recurrence, start, completion, dismissal, restoration, retirement, and supersession.

Completion baselines contain completion time and per-question prompt/result identity when question evidence exists. A site-wide practice can instead freeze business-wide mentioned/analyzed totals. No comparison is displayed until a genuine scheduled outcome evaluator exists.

The checklist is derived from the catalog, published assessments, module freshness, and action history. There is no checklist table.

## Ranking and Improve APIs

The independently versioned ranker orders direct blockers first, then reach, persistence, evidence quality, actionability, lower effort, and stable practice/subject identity. Every active action is returned; the first three are focus actions and the rest are additional recommendations.

`ImproveService` exposes:

- `ListActions(business_id)` — focus actions, additional active actions, freshness, and an empty reason that distinguishes healthy standings, incomplete checks, and insufficient capability.
- `GetAction(action_id)` — action detail, evidence, checklist identity, and all cycles.
- `SetActionStatus(action_id, status, dismissal_reason)` — validated start, completion, dismissal, and restoration transitions.
- `GetChecklist(business_id)` — clickable standing counts, freshness, ordered catalog sections, subject entries, evidence, current action, and compact local history.
- `ListActivity(business_id, limit, offset)` — newest-first paginated lifecycle events.

Reads require subscriber access and the viewer role. Lifecycle mutations require member role. Every query is account scoped.

## Frontend

Improve contains only `/improve/actions`, `/improve/checklist`, and `/improve/activity`; `/improve` navigates to actions. There is no opportunities route or compatibility endpoint.

Next actions displays only active work. The checklist renders every catalog practice, including unregistered ones, and keeps acted-on historical dynamic subjects as “No longer tracked.” Expanded rows explain what is checked, why it matters, evidence and limitations, last successful check, sources/responses, current action, and local cycle history. Standing-count buttons filter rows with keyboard-accessible native controls. A healthy empty state says “All practices OpenSight can currently verify are in good standing,” never that the user has done everything, and the product calculates no score or grade.

Activity states only what happened and when, links to the action and checklist practice, and makes no attribution to subsequent visibility.

## Deferred work

Measured outcome comparison is deferred until a scheduled evaluator can compare frozen baselines with later monitored evidence honestly. The former status-only “later observation” evaluator is not an outcome and does not exist. Operator health reporting over generation, collector, and assessor failure distributions also remains deferred; the underlying records are retained for it.
