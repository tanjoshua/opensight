# Epic 15 — Visibility assessments and opportunities

Design: [09 Opportunities](../design/09-opportunities.md) · Phase 5

## OPP-1 — Catalog, contracts and persistence

As the product, I need visibility practices and evidence to be durable and versioned so new techniques do not require a new architecture.

Acceptance: compiled catalog; collector/assessor/presenter/evaluator contracts; version validation; account-scoped generation, artifact, assessment, opportunity and event tables; durable opportunity identity.

## OPP-2 — Assessment orchestration

As an operator, I need independent modules to survive partial failure without making Temporal histories non-deterministic.

Acceptance: recorded sorted module plan; shared collectors run once; generic assessor activity; partial generation status; replay-safe child stage; stale successful set preservation.

## OPP-3 — Initial practices

As a business, I need checks grounded in current monitored evidence and my owned site.

Acceptance: deterministic search-access practice as the only registered assessor; recurring influential-source and tracked-topic assessors implemented but unregistered pending a quality gate; unmet and highly visible met fixtures; unknown on unreliable inspection.

## OPP-4 — Safe compilation and presentation

As a user, I need a small consistent action queue rather than arbitrary generated advice.

Acceptance: shared eligibility/safety validation; independently versioned ranker; blocker-first order; the derived half of every opportunity rebuilt wholesale per generation in one transaction, capped at five current items and three focus items, with user status/dismissal/baseline/first-seen untouched; typed standard blocks only; evidence and verification limitations included.

## OPP-5 — Lifecycle and outcomes

As a user, I can start, complete, dismiss and restore work without later assessments erasing my decisions.

Acceptance: typed RPC enums; required dismissal reason; frozen completion baseline; append-only idempotent events; site/question evaluators append later observations without causal claims.

## OPP-6 — Opportunities UI

As a user, I can use Improve → Opportunities to understand what to do and why.

Acceptance: three focus cards; Fix first blocker; evidence, affected responses, effort, steps and limitations; more-opportunities, no-longer-detected, completed and dismissed sections so every returned opportunity renders in exactly one place; no confidence or certainty claim on a card; checklist explicitly deferred while assessment data accumulates.

## OPP-7 — Outcome observation

As a user, I can see whether the questions an opportunity affected mention me now, measured against what I completed against.

Acceptance: completion freezes per-question prompt, result and mention state, or the run's mentioned/analyzed counts when the assessment carries no question evidence; readings at the second and fourth analyzed run after completion and never again, threshold-based and idempotent per run ordinal; both sides recomputed over frozen questions still tracked, dropped ones counted, no comparison claimed when none survive; evaluator reads current mentions from the generation's monitoring snapshot and does no I/O; dated readings link every number to its response, state unchanged and negative results in the same shape, and never state or imply causation.

## OPP-8 — Generation health

As the operator, I need quiet assessment degradation to be visible in the weekly check.

Acceptance: `opensight assess health` reports generation status distribution, per-collector failure rate with the latest error, and per-assessor assessment-status distribution over a window; read-only with the same business and date filters as `assess replay`; no thresholds, alerts or stored aggregates; weekly manual check covers it.
