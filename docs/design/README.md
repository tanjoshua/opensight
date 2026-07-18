# OpenSight — Technical Design

Technical design for the [Starter MVP PRD](../prd.md), split into focused docs that build on each other in order.

| # | Doc | Covers |
|---|-----|--------|
| 01 | [Architecture](01-architecture.md) | Stack, ChatGPT data source, self-hosted Temporal, Hetzner deployment, billing-readiness |
| 02 | [Data model](02-data-model.md) | Schema; prompt-replacement lineage; append-only results vs rebuildable analysis |
| 03 | [Onboarding](03-onboarding.md) | Scrape → research → LLM proposal → review/apply; prompt generation rules |
| 04 | [Monitoring](04-monitoring.md) | Weekly RunWorkflow, idempotency, ExecutePrompt, retries, cost model |
| 05 | [Analysis](05-analysis.md) | Extraction, two-pass entity matching, competitor discovery, re-analysis |
| 06 | [API + frontend](06-api-frontend.md) | Endpoints, five sections, response-drawer traceability, shadcn preset |
| 07 | [Cross-cutting](07-cross-cutting.md) | Auth, secrets, deploys, backups, observability, spend guardrails |

Stack: React (Vite, shadcn preset `bLTjNXma`) · Go monolith · PostgreSQL · Temporal (self-hosted) · OpenAI Responses API · single Hetzner VPS.

## Implementation phases

The docs above are the target design; the build is phased so a pilot clinic can start early:

1. **Core loop** — schema + migrations, auth, CLI-seeded profile and prompts, RunWorkflow + ExecutePrompt, Responses section. Pilot-ready.
2. **Analysis** — AnalyzeRun, mentions/citations/metrics, Overview + Prompts + Competitors sections.
3. **Self-serve polish** — onboarding automation (03), competitor triage + alias approval UX, methodology page.
