# Epic 10 — Onboarding Automation (ONB)

Name + website → generated profile + 20 prompts → review → apply → first run. Phase 3.

---

## ONB-1 — FetchSite activity (SSRF-guarded)

As the developer, I want a safe site fetcher, so that onboarding can read a clinic's website without being an SSRF vector.

- [x] Fetches homepage + high-value paths (`/about`, `/services`, `/team`, `/doctors`, `/contact`, homepage-nav links, sitemap.xml if present); HTML stripped to text, capped ~50KB total.
- [x] SSRF guards: http(s) only; DNS resolved with private/link-local/metadata ranges refused; redirects capped and re-validated per hop; response size and time capped. No headless browser.
- [x] Tests cover: private-IP refusal, redirect-to-private refusal, size cap.

Deps: RUN-3 (worker infra) · Phase 3 · Ref: design 03 (GenerateProfileWorkflow step 1)

## ONB-2 — ResearchBusiness activity

As the developer, I want one web_search call on the business, so that aliases the site omits get caught.

- [x] Single OpenAI `web_search` call via the existing `PromptRunner` plumbing, on business name + location hints.
- [x] Output targets: aliases (former/Chinese/colloquial names — **organization trading identities only**; never a person's name unless genuinely part of the trading name) and directory listings.

Deps: RUN-1 · Phase 3 · Ref: design 03 (step 2)

## ONB-3 — ProposeProfile activity

As the developer, I want a structured-output proposal call with validation, so that generated profiles are well-formed before a user sees them.

- [x] Single LLM call producing the design-03 payload: `low_confidence`, profile (name, aliases, category, services, location with **country required**), prompts with `kind` (category|service|condition|location).
- [x] Prompt rules enforced by validation: **prompts never contain the business name**; count = `plan.prompt_limit` (not hardcoded 20); mixed kinds; phrased as real consumer questions.
- [x] Validation failure (wrong count, empty fields, name leakage) → one retry with errors appended.

Deps: ONB-2 · Phase 3 · Ref: design 03 (step 3, Prompt generation rules)

## ONB-4 — GenerateProfileWorkflow + proposal API

As a clinic user, I want to submit my name and website and get a proposal, so that setup takes minutes, not a form.

- [x] `POST /api/businesses` inserts business (status `draft`) and starts the workflow; the UI polls proposal status while generation runs.
- [x] Failure posture: FetchSite fails → proceed research-only with `low_confidence: true`; both sources fail → workflow fails and UI offers manual setup (same review screen, empty).
- [x] Proposal written to `profile_proposals` (status `pending`) — generation **never** writes `businesses` columns.
- [x] `GET /businesses/:id/proposal` (status + payload), `POST /businesses/:id/proposal/regen` (draft only: discard + regenerate).

Deps: ONB-1, ONB-3 · Phase 3 · Ref: design 03 (Flow, Failure posture, API surface)

## ONB-5 — Review & apply UI

As a clinic user, I want to review and edit everything before monitoring begins, so that confirmed values are mine, not the machine's.

- [x] `/onboarding` flow: create form → progress state polling proposal status → review screen with **every** proposed value editable (profile fields, aliases, services, location, each prompt); `low_confidence` nudges harder review.
- [x] Client submits the final edited payload; server takes it verbatim (no merge).
- [x] Regenerate available while draft; manual-setup path when generation failed.

Deps: ONB-4, WEB-1 · Phase 3 · Ref: design 03 (Review and apply), PRD §3, §8

## ONB-6 — Apply transaction + first run

As a clinic user, I want approval to start monitoring immediately, so that I see first results without waiting a week.

- [x] `POST /businesses/:id/apply` in one transaction: update business columns, insert prompts (all `active`), proposal → `applied`, business → `active` — the **only** path that writes profile values to `businesses`.
- [x] Then: create the weekly Temporal Schedule and trigger the first run now (`trigger=initial`); first-run-midweek + scheduled-run coexist via distinct `scheduled_for` dates.
- [x] No regenerate after activation; post-activation profile changes only via Setup PATCH.

Deps: ONB-5, RUN-5 · Phase 3 · Ref: design 03 (Review and apply), 04 (RunWorkflow idempotency)
