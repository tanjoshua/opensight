# Epic 15 — Improve and visibility assessments

Design: [09 Improve](../design/09-opportunities.md) · Phase 5

## IMP-1 — Catalog and assessment truth

As a user, I can see every visibility practice OpenSight knows about, including practices it cannot yet assess.

Acceptance: catalog modes, subject scope, and recommendation capability; stable `business` identity; missing websites are unverifiable; all catalog entries appear with final checklist standings and mode-specific labels.

## IMP-2 — Partial-failure-safe publication

As an operator, I can publish independent assessor results without erasing valid prior state when another assessor fails.

Acceptance: succeeded/failed/skipped module outcomes; one atomic assessment/action/generation publication; failed/skipped scopes remain stale; dynamic subjects retire only following assessor success; running and failed generations are excluded.

## IMP-3 — Repeatable action cycles

As a user, I can start, complete, dismiss, and restore an action while preserving every prior cycle.

Acceptance: stable recommendation key and numbered cycles; active cycles update in place; completed and dismissed cycles are immutable; verified regressions and materially changed recommendations recur; identical dismissed recommendations remain suppressed; automatic retirement and supersession create activity events; completion baseline is frozen once.

## IMP-4 — Next actions

As a user, I can focus on the most relevant active work.

Acceptance: three focus actions followed by all additional active recommendations; only open/in-progress cycles; evidence, sources, effort, steps, limitations, and mutation feedback; healthy, incomplete-check, and insufficient-capability empty states.

## IMP-5 — Visibility checklist

As a user, I can understand OpenSight’s current standing for every practice without a misleading score.

Acceptance: ordered catalog sections; clickable standing counts; expandable dynamic subjects; historical acted-on subjects remain “No longer tracked”; evidence, limitations, last successful check, sources/responses, current action, and local cycle history; healthy copy is limited to what OpenSight can verify; no score or grade.

## IMP-6 — Activity history

As a user, I can review what happened to action cycles over time.

Acceptance: newest-first pagination; created/recurring, started, completed, dismissed, restored, retired, and superseded events; action and checklist links; no causal attribution.

## IMP-7 — Deferred measured outcomes and health

As the product, we do not present a re-check as evidence that completed work changed visibility.

Acceptance: status-only later observations removed; future-compatible completion baselines retained; genuine scheduled outcome evaluation and operator generation-health reporting remain deferred.
