# Design 03 — Onboarding Pipeline

Depends on: [01 Architecture](01-architecture.md), [02 Data Model](02-data-model.md) (`businesses`, `profile_proposals`, `prompts`)

## Flow

```mermaid
sequenceDiagram
    actor U as User
    participant API as Go API
    participant T as Temporal
    participant W as Worker
    U->>U: Enter business name, then website (or explicitly continue without one)
    U->>API: BusinessService.CreateBusiness {name, website}
    API->>API: insert businesses (status=draft)
    API->>T: start GenerateProfileWorkflow
    W->>W: FetchSite (activity)
    W->>W: ProposeProfile (activity, web_search + structured output; profile only, no prompts)
    W->>W: insert profile_proposals (status=pending)
    U->>API: BusinessService.GetProposal (poll until ready)
    U->>U: review + edit Business, then Services
    U->>API: BusinessService.GenerateQuestions {business_id, profile} (on leaving Services)
    API->>U: prompt_limit customer questions, grounded in the approved services
    U->>U: review + edit Customer questions
    U->>API: BusinessService.ApplyProposal {final payload}
    API->>API: write businesses, insert plan.prompt_limit prompts, proposal→applied, status=active
    API->>T: create weekly Schedule + trigger first run now
```

Generation is LLM work with agentic web browsing plus a possible validation retry, so end-to-end runtime is on the order of a minute, occasionally more. GenerateProfileWorkflow runs to completion with no fixed deadline; the UI polls proposal status and, while generating, shows a stage-driven step list (reading the website → researching and drafting) rather than assuming a fixed budget.

The entry screen asks for the business name and website in two short steps. Enter on the name advances to the website rather than starting generation; the website step labels the field optional but recommended and makes proceeding without one an explicit action. The submitted research website remains visible during generation and review. Adding, changing, or clearing it updates the draft, terminates any in-flight generation, discards the pending proposal, and regenerates from the corrected input, so recovery is available before activation rather than only later in Settings.

Customer questions are deliberately generated in a second, separate step rather than alongside profile research: they must reflect the service list the user actually approves (editing Services after an upfront generation would otherwise leave stale questions), and unlike profile research they need no web browsing. Splitting them out keeps every `GenerateQuestions` call small and inexpensive while allowing a quality-focused model because the resulting questions become the business's long-lived measurement instrument.

## GenerateProfileWorkflow

Two activities, each independently retryable:

1. **FetchSite** — plain HTTP GET of the homepage plus obvious high-value paths (`/about`, `/services`, `/team`, `/doctors`, `/contact`, and anything linked from the homepage nav; sitemap.xml if present). HTML stripped to text, capped (~50KB total). **No headless browser in MVP** — clinic sites that render nothing without JS produce empty text and the combined call browses instead. Fetching is SSRF-guarded: http(s) only, DNS resolved with private/link-local/metadata ranges refused, redirects capped and re-validated per hop, response size and time capped. It is the free, deterministic, **primary** evidence source; feeding its text into the next call also reduces the model's own billable browsing.
2. **ProposeProfile** (combined research + draft) — a single OpenAI Responses call through the official Go SDK on a reasoning model that has the `web_search` tool attached with `tool_choice: "required"` **and** a strict structured-output JSON schema. The model searches/opens pages, then emits schema-conforming JSON in one call. It confirms the business identity, specialist category, services and location; finds organization-only trading aliases; and checks relevant directories or registries. `site_text` is primary evidence when present; when it is empty or insufficient the model opens the supplied website itself. The concise instructions retain only business rules the schema cannot express: evidence priority, evidence-backed category/services, organization-only aliases, whole-profile confidence, and retry correction. This call produces the business profile only — no customer questions; those are `GenerateQuestions`' job, below. If output fails validation (empty required fields), it retries once with the validation errors appended.

`web_search` on reasoning models supports agentic browsing (`open_page`/`find_in_page`), so it can read a specific URL — including JS-only sites FetchSite can't render. FetchSite is nonetheless kept as the deterministic, free primary source; the model browses to fill its gaps. Whether FetchSite can be retired entirely is an open question pending observed `open_page` reliability in production (the spike showed it opening the exact site URL on every run).

The onboarding model is configurable via `OPENAI_ONBOARDING_MODEL` (separate from the monitoring/analysis model), defaulting to `gpt-5.6-terra`: this is a one-time, quality-sensitive research pass whose profile and aliases seed future monitoring. The repeated high-volume analysis path remains on `gpt-5.6-luna`.

**Sources.** Under strict JSON output the response carries no `url_citation` annotations (there is no prose to annotate — empirically zero across every spike run), so `sources` is derived server-side from the `web_search_call` **actions**: the pages the model opened (`open_page`/`find_in_page` URLs), normalized (utm stripped, host lowercased) and deduped. This is a `sources` array on the proposal payload, populated by us and shown read-only in review; it is never part of the model's own schema and is not persisted to `businesses`.

The workflow exposes its current stage (`fetching_site` | `drafting`) via a Temporal query (`generation-stage`), advanced between activities so a retry never regresses it. `BusinessService.GetProposal` includes `stage` while status is `generating`, degrading to omission if the query fails (e.g. worker briefly unavailable, a pre-deploy run without the handler); the poll never fails on a stage-query error.

Failure posture: FetchSite is no longer a success gate — its text feeds the combined call, and on failure the model still has its own web research. If FetchSite fails, force `low_confidence: true` **unless** the model's web-search actions show an `open_page` on the business website's own domain (it read the site itself), in which case the model's own `low_confidence` judgement stands. The workflow fails only when the combined call fails outright (provider error after Temporal retries) or never validates after the in-activity retry — the UI then offers manual setup (same review screen, empty).

## GenerateQuestions (customer questions)

Unlike the profile, customer questions are not generated inside `GenerateProfileWorkflow`. `BusinessService.GenerateQuestions` is a plain synchronous RPC the review screen calls on demand when the user leaves the Services step (see "Review and apply" below) — there is no Temporal workflow or activity involved, since there is nothing to retry across a process restart: a failed call just leaves the user on the Services step to try again or add questions by hand.

The call is a single structured-output OpenAI Responses call with **no** `web_search` tool (and so, unlike ProposeProfile, produces no `sources`): it drafts purely from the already-reviewed `category`, `services`, and `city` (never `address`/`area` — city-only geography, same as before this split). It uses `OPENAI_QUESTIONS_MODEL`, defaulting to `gpt-5.6-terra`, separately from both `OPENAI_ONBOARDING_MODEL` and `OPENAI_ANALYSIS_MODEL`. This is a one-time, high-leverage generation step: the questions become the business's ongoing measurement instrument. Same validation-retry posture as `ProposeProfile` (`llm.GenerateQuestionsWithRetry`, one retry with the prior output and validation errors appended). See "Customer-question generation rules" below for the content rules it enforces.

`BusinessService.ApplyProposal` never trusts the client's question count or content: it revalidates the submitted payload's profile (`llm.ValidateProfile`) and prompts (`llm.ValidateQuestions`, with `PromptLimit` from the account's plan, never from the request) before writing anything — the same defense-in-depth posture as the rest of apply.

## Proposal payload (`profile_proposals.payload`)

```jsonc
{
  "low_confidence": false,
  "profile": {
    "name": "…",
    "aliases": ["…"],
    "category": "…",            // e.g. "orthopaedic clinic"
    "services": ["…"],
    "location": { "address": "…", "area": "…", "city": "Singapore", "country": "SG" }
  },
  "prompts": [],               // always empty here — GenerateQuestions fills these client-side, later
  "sources": [ { "url": "…", "title": "", "domain": "…" } ]  // server-populated, read-only
}
```

`sources` is omitted when the model opened no pages. It is computed by us from the model's web-search actions (not part of the model's schema), so it is only informational — apply never persists it into `businesses`. `prompts` stays on this shared shape because `ApplyProposal`'s payload (the client's final, edited draft, sent back over the same `ProposalPayload` message) carries the approved questions through to persistence.

### Customer-question generation rules

The `plan.prompt_limit` questions are the product's measurement instrument, so generation is opinionated:

- The concise generation instruction asks for varied, natural prompts a prospective customer might ask an AI assistant to surface provider recommendations in the given city. Every prompt is based on the confirmed category and one or more confirmed services.
- Questions never contain the business name. They simulate a prospective patient who doesn't know the business exists — that is what "visibility" means. `ValidateQuestions` enforces this after generation. (Users can still add branded questions manually if they insist.)
- Geography comes from the business's city only — never its neighbourhood, district, street, or landmark (`address`/`area` are not sent for question generation).
- Count comes from `plan.prompt_limit`, not a hardcoded 20.

## Review and apply

- The review screen is a three-step, in-memory draft: **Business → Services → Customer questions**. Back/next navigation retains edits, and each step must be valid before advancing. The research website is shown above the steps with an add/change action; changing it regenerates the proposal and, when the user has local edits, first confirms that those edits will be replaced. Business contains identity, aliases, location, and a collapsed read-only list of research sources; a global low-confidence warning remains visible throughout. Services owns the editable service list. Customer questions shows an exact `current of prompt_limit` count and requires that exact count before approval.
- Leaving Services is where questions actually get generated: the review screen calls `GenerateQuestions` with the in-memory profile at that point (not the original proposal), so the questions are always grounded in what the user has just confirmed. A client-side fingerprint of `category`/`city`/`services` is cached against the generated set so a plain Back/Next round trip reuses it instead of re-calling the RPC; the fingerprint changing (a service edited, or the category changed back on the Business step) triggers a fresh call, with a confirmation first if the user had manually edited the question list. If generation fails, the user can retry or skip straight to Customer questions and add questions by hand — manual entry is always available, exactly as before this split.
- Nothing is committed while moving through the review steps. The client sends back the **final edited payload** only when the user selects **Approve setup and start monitoring** — the server does not merge, it takes the submitted values verbatim (they've been reviewed by definition). The approval copy states that the first check starts after approval and monitoring then runs weekly; it does not imply that step navigation saves the draft to the server.
- `AccountService.GetAccountContext` exposes the account's `Plan` message (`code`, `prompt_limit`, `run_interval`, `platforms`), not a bare `prompt_limit` int — the SPA's plan projection reads `plan.prompt_limit`. Per-step validation covers required profile fields, country code, non-empty list entries, prompt text, and exact plan prompt count. Validation gates navigation and submission but does not normalize or rewrite the payload: the accepted payload is still sent verbatim.
- **Apply is the only path that writes profile values to `businesses`** (the 02 invariant). It runs in one transaction: update business columns, insert prompts (all `active`), mark proposal `applied`, set business `active`. Then: create the Temporal weekly Schedule and **trigger the first run immediately** — PRD success criterion 4 ("view the first ChatGPT results") shouldn't wait a week.
- **Regenerate** is allowed while the business is `draft`: discard the pending proposal and re-run `GenerateProfileWorkflow` (profile research only — it no longer touches questions). If the user has edited the in-memory review draft, the UI warns that regeneration will replace those edits before proceeding. After activation there is no regenerate — profile changes are manual edits in Setup (PRD: confirmed values are never auto-overwritten), and prompt changes go through the replace flow (02).

## API surface (this slice)

```
BusinessService.CreateBusiness       → create draft, start workflow
BusinessService.GetProposal          → status + payload when ready
BusinessService.RegenerateProposal   → optionally replace research website, stop current generation, discard + regenerate (draft only)
BusinessService.GenerateQuestions    → on-demand customer questions from the reviewed profile (draft only)
BusinessService.ApplyProposal        → apply final payload, activate, schedule
```

## Open questions (owned by later increments)

- **05**: whether `aliases` collected here are sufficient for mention matching, or matching needs its own enrichment loop.
- **06**: review-screen UX details (per-field confidence display).
