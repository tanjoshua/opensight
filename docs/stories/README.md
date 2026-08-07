# OpenSight — MVP Story Backlog

Backlog from empty repo to the [Starter MVP](../prd.md), built on the [technical design](../design/README.md). One file per epic, numbered in rough execution order. Stories are sized for a solo dev (~0.5–3 days each) and carry: ID, user story, acceptance criteria, dependencies, phase, design reference.

There are **no users until the full MVP is complete** — phase milestones are internal checkpoints, not customer moments.

## Epics

| # | Epic | IDs | Stories | Phase |
|---|------|-----|---------|-------|
| 00 | [Validation spike](00-validation-spike.md) | SPK | 1 | 1 |
| 01 | [Foundation & infra](01-foundation.md) | FND | 6 | 1 |
| 02 | [Core schema & store](02-schema.md) | SCH | 5 | 1 |
| 03 | [Auth & accounts](03-auth.md) | AUTH | 4 | 1 |
| 04 | [Run pipeline](04-run-pipeline.md) | RUN | 6 | 1 |
| 05 | [App shell & Responses UI](05-responses-ui.md) | WEB | 5 | 1 |
| 06 | [Ops & checkpoint](06-ops.md) | OPS | 3 | 1 |
| 07 | [Analysis pipeline](07-analysis.md) | ANA | 6 | 2 |
| 08 | [Metrics API](08-metrics-api.md) | MET | 6 | 2 |
| 09 | [Insight UI](09-insight-ui.md) | INS | 4 | 2 |
| 10 | [Onboarding automation](10-onboarding.md) | ONB | 5 | 3 |
| 11 | [Self-serve polish](11-selfserve-polish.md) | POL | 7 | 3 |
| 12 | [Protobuf/Connect RPC migration](12-rpc-migration.md) | RPC | 9 | 3 |
| 13 | [Runs UI](13-runs-ui.md) | RUNS | 6 | 3 |
| 14 | [Self-serve signup & billing](14-billing.md) | BILL | 12 | 4 |
| 15 | [Improve & visibility assessments](15-opportunities.md) | IMP | 7 | 5 |

**93 stories: Phase 1 = 30, Phase 2 = 16, Phase 3 = 27, Phase 4 = 12, Phase 5 = 8.**

- **Phase 5 — Visibility improvement (epic 15).**
  🎯 Milestone: every analyzed run accumulates versioned assessments; active unmet practices compile into a safe three-item focus queue with durable lifecycle state and later observations.

## Phases and milestones

- **Week zero — validation spike (epic 00).** Go/no-go on the core assumption before any scaffolding: the API names specific local clinics with citations.
- **Phase 1 — Core loop (epics 01–06).**
  🎯 **Internal checkpoint: real weekly ChatGPT responses viewable end-to-end** — a CLI-seeded business runs weekly on production and the Responses section shows raw responses, run status, and errors behind login. Backups landing offsite; the restore drill (OPS-2) is MVP acceptance, not a checkpoint gate.
- **Phase 2 — Analysis (epics 07–09).**
  🎯 Milestone: mentions, sentiment, citations, and competitors are derived from stored responses; Overview, Prompts, and Competitors sections show visibility metrics where every number opens the underlying response. Extraction quality gate (ANA-2) passed before the phase is called done.
- **Phase 3 — Self-serve polish (epics 10–11).**
  🎯 Milestone: all PRD §8 success criteria pass without operator involvement — enter name + website, review generated setup, approve prompts, manage prompts/competitors, methodology + privacy pages live. **This is the MVP; the first real users onboard after this point.**
- **Phase 4 — Commercial launch (epic 14).**
  🎯 Milestone: a stranger signs up from the marketing site, pays S$50/month, onboards and sees their first run — with no operator involved and no free LLM spend. Cancellation pauses monitoring and leaves history readable; reactivation resumes it.

## Execution order

SPK-1 runs before everything. Within Phase 1, epics 01→02→03 are sequential foundations; 04 (backend pipeline) and 05 (frontend) can interleave after 02; 06 closes the phase. Phase 2: 07 before 08 before 09 (data → API → UI), though 09 pages can start against 08 endpoints one at a time. Phase 3 runs **10 (onboarding) before 11 (polish)**: with no mid-build users there is no live-customer pull toward polish, and ONB is the technically riskiest epic (SSRF fetcher, generation quality, ~90s end-to-end target), so its unknowns should surface first; POL's dependencies on Phase-2 UI are satisfied either way.

Epic 12 (RPC) is infra work independent of the product epics — it can run any time after 08 (an
existing, stable API surface to migrate) and before no particular milestone; it does not gate the
Phase 3 MVP milestone.

Epic 14 (billing) runs after the Phase 3 MVP milestone: it takes payment for a product that must
already be worth paying for. BILL-1 (schema) and BILL-2 (Stripe adapter) gate the rest of the epic;
BILL-7 (spend backstop) must land before any real card is charged.

Explicitly **not** in this backlog: an admin portal and its account-management actions (including
changing comp status), additional platforms, alerts, competitor merge, Prometheus/metrics, multi-VPS.
