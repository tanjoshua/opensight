# Epic 11 — Self-Serve Polish (POL)

Prompt management, competitor triage, Setup, methodology. Phase 3 — closes the remaining PRD §8 criteria.

---

## POL-1 — Prompt add & replace flow

As a clinic user, I want to edit or replace prompts with an unskippable warning, so that I control my measurement instrument without corrupting history.

- [x] `POST /businesses/:id/prompts` (409 at `plan.prompt_limit`); `POST /prompts/:id/replace` requires `{text, confirmed: true}` — retire old + insert new with `replaces_prompt_id` (text immutable; edit and replace are the same operation).
- [x] Modal states exactly: "history for the old prompt stays viewable; the new prompt starts a fresh trend"; API rejects without `confirmed: true`.
- [x] Replacement takes effect next run (workflow snapshots at start); new prompt = new trend series in all charts.

Deps: MET-3, INS-2 · Phase 3 · Ref: design 06 (Prompts — replace flow), 02 (Prompts), PRD §4

## POL-2 — Prompt-set-change trend markers

As a clinic user, I want trend charts to mark when my prompt set changed, so that a prompt change never reads as a visibility change.

- [x] Overview weekly trend (and prompt sparklines where sensible) render markers derived from prompt created/retired dates.
- [x] Marker tooltip names the change ("1 prompt replaced").

Deps: POL-1, INS-1 · Phase 3 · Ref: design 06 (Overview)

## POL-3 — Competitor triage & manual add

As a clinic user, I want one-click track/dismiss and manual competitor entry, so that the competitor list reflects who I actually care about.

- [x] `POST /competitors/:id/track`, `POST /competitors/:id/dismiss`; `POST /businesses/:id/competitors` manual add {name, aliases?, website?} (source `manual`).
- [x] Triage-first UI on the discovered list (INS-3): one-click track/dismiss; dismissed collapse but are recoverable with history intact.
- [x] Re-tracking a dismissed competitor restores full history (mentions were never deleted).

Deps: INS-3 · Phase 3 · Ref: design 06 (Competitors), PRD §6

## POL-4 — Suggested-alias approval

As a clinic user, I want to approve or reject LLM-suggested name variants, so that matching keys are mine to control.

- [x] Competitor detail lists `suggested_aliases` with one-click approve/reject; approve promotes to `aliases` (future matching goes to the exact pass), reject removes the suggestion.
- [x] Until approved, variants keep being re-judged by the LLM pass each run (no silent promotion anywhere).

Deps: ANA-5, INS-3 · Phase 3 · Ref: design 05 (Phase 2 step 3), 06 (Competitors)

## POL-5 — Setup section

As a clinic user, I want to manage my profile, prompts, and competitor configuration in one place, so that post-activation changes are self-serve.

- [x] `GET /businesses/:id` (profile + read-only plan info); `PATCH /businesses/:id` manual edits — the only post-activation profile path; country stays required.
- [x] Setup page: profile editor, business-alias editing, prompt-management entry point (POL-1), competitor alias editing, plan display. No regenerate after activation.

Deps: POL-1, POL-3 · Phase 3 · Ref: design 06 (Setup), PRD §7

## POL-6 — "How we measure" methodology page

As a clinic user, I want a plain statement of how results are produced, so that I can trust (and correctly discount) the numbers.

- [x] Short page linked from Overview: results come from the OpenAI API with web search as a **proxy for consumer ChatGPT**; no memory/personalization, possibly different model routing; exact model recorded per run; visibility counts analyzed valid responses only.
- [x] Linked wherever the proxy caveat matters (Overview, drawer model line).

Deps: INS-1 · Phase 3 · Ref: design 06 (Overview), 01 (D1 honest limitation)

## POL-7 — Privacy page

As a clinic user, I want a plain-language privacy page, so that a medical business can trust where its data sits before paying.

- [x] Static page (~0.5 day), PDPA-aware plain-language copy: what we store (user emails, business profiles), the hard rule that **no patient-identifiable data ever enters the system**, hosting region, retention.
- [x] Linked from the app footer/shell and from the methodology page (POL-6) — same genre of trust page.
- [x] Ships with the MVP: "before first paying customer" (design 07) coincides with MVP completion, so converting a customer never waits on writing a webpage.

Deps: POL-6 · Phase 3 · Ref: design 07 (Data protection)
