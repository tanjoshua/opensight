# Epic 10 — Onboarding Automation (ONB)

Name + website → generated profile + 20 prompts → review → apply → first run. Phase 3.

---

## ONB-1 — FetchSite activity (SSRF-guarded)

As the developer, I want a safe site fetcher, so that onboarding can read a clinic's website without being an SSRF vector.

- [x] Fetches homepage + high-value paths (`/about`, `/services`, `/team`, `/doctors`, `/contact`, homepage-nav links, sitemap.xml if present); HTML stripped to text, capped ~50KB total.
- [x] SSRF guards: http(s) only; DNS resolved with private/link-local/metadata ranges refused; redirects capped and re-validated per hop; response size and time capped. No headless browser.
- [x] Tests cover: private-IP refusal, redirect-to-private refusal, size cap.

Deps: RUN-3 (worker infra) · Phase 3 · Ref: design 03 (GenerateProfileWorkflow step 1)

## ONB-3 — ProposeProfile activity (combined research + draft)

As the developer, I want one call that researches the business on the web and emits a validated structured proposal, so that generated profiles are accurate and well-formed before a user sees them.

- [x] Single OpenAI Responses call on a reasoning model (`OPENAI_ONBOARDING_MODEL`) with the `web_search` tool attached (`tool_choice: "required"`, agentic `open_page`/`find_in_page`) **and** a strict structured-output schema — the model gathers its own evidence and drafts in one call. `site_text` from FetchSite is primary evidence; the model opens the site itself when it is empty/thin.
- [x] Research targets: specialty/category, aliases (former/Chinese/colloquial names — **organization trading identities only**; never a person's name unless genuinely part of the trading name), and directory listings.
- [x] Produces the design-03 payload: `low_confidence`, profile (name, aliases, category, services, location with **country required**), text-only prompts, and a server-derived read-only `sources` list (the pages the model opened).
- [x] Prompt rules enforced by validation: **prompts never contain the business name**; count = `plan.prompt_limit` (not hardcoded 20); phrased as varied real consumer questions.
- [x] Validation failure (wrong count, empty fields, name leakage) → one retry with errors appended.

Deps: ONB-1, RUN-1 · Phase 3 · Ref: design 03 (GenerateProfileWorkflow step 2, Prompt generation rules)

## ONB-4 — GenerateProfileWorkflow + proposal API

As a clinic user, I want to submit my name and website and get a proposal, so that setup takes minutes, not a form.

- [x] `POST /api/businesses` inserts business (status `draft`) and starts the workflow; the UI polls proposal status while generation runs.
- [x] Failure posture: FetchSite fails → proceed on the model's own web research, forcing `low_confidence: true` unless the model opened the site itself; the combined call failing outright (or never validating) fails the workflow and the UI offers manual setup (same review screen, empty).
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
