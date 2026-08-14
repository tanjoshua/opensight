# Design 09 — Improve: site audit and evidence-derived findings

Depends on: [02 Data model](02-data-model.md), [04 Monitoring](04-monitoring.md), [05 Analysis](05-analysis.md), [06 API + frontend](06-api-frontend.md)

## Product model

Improve has two deliberately separate pipelines. The site audit is a closed, stable list of deterministic checks against the customer's own website. Findings are an open set of evidence-derived work items produced from the audit and monitoring corpus. The checklist presents the former; Next actions presents the latter.

This separation keeps the checklist comprehensive and identical between visits without forcing changing citation domains or future advice types into a catalog-practice lifecycle.

## Site audit

The audit catalog is a flat ordered list of checks. Each check declares a stable key, presentational group (`Access`, `Structure`, or `Identity`), assertion title, methodology, whether it is informational or blocking, optional fix steps, an optional parent check, and priority from 1 to 3. Groups carry explanatory copy and references but no status of their own.

The catalog holds no industry knowledge, and that is what makes it generalize: reachability, robots directives, canonical URLs, and structured-data presence are properties of the web rather than of a trade, so the same seventeen assertions are correct for a clinic and a software company. Anything whose right answer changes with the industry belongs in the open half, derived from evidence.

An audit emits exactly one result per catalog check with `PASS`, `FAIL`, `COULD_NOT_VERIFY`, or `NOT_APPLICABLE`, plus evidence detail and optional sources. Informational checks, such as GPTBot access, report legitimate choices and do not contribute to passed or failed counts. A failed check produces a finding only when it is non-informational and declares fix steps.

A check may declare a parent whose failure necessarily fails it too: the three structured-data field checks depend on structured data being present at all. The checklist still reports every one of them, because a user reading it wants every assertion — but the work queue folds a dependent failure into its parent rather than asking five times for one edit. Folding is conditional on the parent having actually failed, so a site that publishes JSON-LD and omits one field still gets that specific action. Telephone deliberately declares no parent: a `tel:` link satisfies it with no structured data at all, so it has a remedy of its own. The dependency graph is a forest rooted in reachable checks, drift-guarded by a test requiring a parent to be declared before its dependents.

The structured-data action states the exact block to publish. Its steps are built from the confirmed business profile — name, website, and postal address as the user reviewed them at onboarding — and rendered as paste-ready JSON-LD, followed by a step to narrow the generic `LocalBusiness` type and a step naming the fields the profile cannot supply, such as opening hours. No value is ever invented: with no confirmed profile the action falls back to the catalog's generic fix, because generic advice beats a template of placeholders a user might publish unedited. Resolving a specific schema.org type per industry would put industry knowledge back into the deterministic half, so the type is generic and narrowing it is the user's step.

The 17 checks cover:

- access: reachability, authentication barriers, page indexing, OAI-SearchBot, ChatGPT-User, and informational GPTBot access;
- structure: sitemap publication, robots sitemap declaration, canonical URL, unique titles, business name in the homepage title, and meta description;
- identity: structured-data presence and business type, telephone, address, and opening hours.

The site scan requests each page once and follows redirects through the same SSRF-safe URL validation as direct requests. A registrable apex and its exact `www` alias are treated as one site: if the configured entry page is unusable or transiently unreachable, the scan tries its sibling once, and a successful sibling becomes the origin for the rest of the scan. Navigation and sitemap URLs may use either alias or an older HTTP spelling, but an HTTPS crawl is never downgraded. Arbitrary subdomains remain separate. Title, first heading, meta description, canonical URL, telephone links, bounded JSON-LD, indexing directives, and text are collected during that parse. `robots.txt` is read once from the host that served the pages and yields the three crawler verdicts and sitemap directives. Page and robots failures remain distinct: an unreadable robots file makes only robots-derived checks unverifiable.

A crawl failure is still a publishable audit. It records the failure and emits every check as unverifiable. This is evidence about the website, not an activity failure.

## Findings

A finder receives the current audit and a monitoring snapshot containing the latest four analyzed runs. It returns self-describing findings with a readable deterministic key, source, category, title, explanation, steps, evidence identifiers and URLs, blocking flag, reach, and priority.

Category is the kind of change a finding asks for, and the only axis Next actions filters on: getting listed on someone else's directory is a different afternoon's work from editing your own site. Every checklist group is also a category — a site-audit finding takes the failed check's group — so the deterministic half needs no second taxonomy: `access` ("Website access"), `structure` ("Site structure"), `identity` ("Business details"). The open half adds the two categories no check can produce: `content` ("Website content") for writing on the customer's own site, and `listings` ("Listings & directories") for work on a third party's. Source stays internal provenance; category is what the user acts on. A later finder introduces a category by adding one catalog entry, and the test guards the direction that must hold — every group is a category, not every category a group.

Keeping content out of Business details is what makes the filter mean something. Pasting a JSON-LD block and drafting a services page are not the same afternoon's work, and separating them also keeps reach from distorting the queue: a writing job carries per-answer reach and a markup fix structurally cannot, so ranking them in one bucket would always bury the cheap certain fix under the expensive uncertain one.

The finder set:

- `site-audit` turns each failed actionable check into a finding such as `site-audit:pages_allow_indexing`; its detail is the check evidence and its steps come from the check catalog.
- `citation-gap` groups cited external domains across answers where the business was absent, but counts recurrence and reach only for distinct absent-business results containing a citation actually linked through `mention_citations` to a competitor. Unlinked occurrences never inflate the candidate. It ranks candidates by the extracted competitor names each source was **cited for**; those names are evidence labels and are not presented as proof that aliases are distinct businesses. It inspects cited pages through the SSRF-safe bounded researcher, capped at two inspected URLs per candidate so one domain cannot spend the whole run budget, and keeps only a source it could read and on which the business is genuinely absent. An unreadable source, exhausted research budget, source with no competitor link, or existing business listing produces no finding.
- After all bounded page inspection, one `OPENAI_IMPROVE_MODEL` call classifies the readable candidates as `competitor_owned`, `third_party`, or `unknown` from inspected page identity/content and linked competitor names. The same call receives each exact stored citation passage with its monitored question, compares selected competitor-owned claims with the bounded customer-site crawl, and emits content opportunities. Its compact prompt states the product objective and a five-part judgment rubric — direct, comparable, grounded, proportionate, and useful — while the structured interface keeps observation, bounded site state, and suggested action distinct. An opportunity is a hypothesis from an observed answer pattern, never a claim that missing content caused omission or that publishing it will improve visibility.
- Each opportunity carries a unique 3–64 character semantic slug naming its enduring publishing job. Earlier model-defined opportunities are supplied as reuse candidates: the model keeps an exact prior slug when current evidence supports the same job, drops it when the opportunity is gone, and creates a new slug for genuinely different work. The schema rather than prose owns formatting constraints, and no example taxonomy anchors the model's grouping.
- Validation is split by what the product reads. Source classifications are load-bearing — every listing decision reads them — so output must cover every candidate exactly once with a known verdict; failure retries once and then leaves the prior publication current. Opportunities fail independently: each needs a valid unused slug, neutral title, observation, bounded site state, suggested action, known coverage, grounded site evidence, and selected competitor-owned claim references that state the supported point. `partial` requires site evidence that occurs in the crawl after punctuation-insensitive normalization; `absent` requires none. A bad opportunity is dropped without costing the rest of the assessment.
- A `third_party` or `unknown` candidate remains `citation-gap:<domain>` in Listings & directories. Competitor-owned evidence is grouped across domains into model-defined `competitor-content:<topic-slug>` findings in Website content. There is no predefined content taxonomy: the model merges claims when they support the same coherent publishing job and keeps materially different jobs separate. Evidence already communicated clearly in the pages read produces no opportunity; an unreadable customer site also produces none because absence cannot be evaluated. Unknown customer facts stay conditional, and suggested actions never ask the user to copy content or publish unsupported facts or outcomes.
- A content finding carries up to three monitored-answer passages, each with the source cited alongside it, and up to two confirmed passages from the customer's site. Quotations let the user check the observation without turning it into causal proof. Answer passages are chosen one domain at a time before a second is taken from any domain. Site-audit findings carry no comparison.
- `selection-criteria` reads the sources the previous finder drops for zero lift. A citation attached to a criterion sentence rather than to any business still reveals what the answer selects on — a licensing register, a specialty accreditation, a piece of equipment — so the finder extracts that criterion and asks the customer to make it visible and machine-readable on their own site. It keeps a criterion only when its source recurs across questions or runs, and it never recommends getting listed on the cited source: an accreditation body or regulator is evidence about how the answer chooses, not a directory to join. Its category is therefore `identity` or `structure`, never `listings`.

Findings sort by blocking first, then category order, then affected-question reach descending, priority ascending, and stable key. Category order is the catalog sequence, which runs easiest-to-act-on first and puts listings last: a change to the customer's own site is entirely within their control, while a listing depends on a third party accepting the entry. That tier deliberately outranks reach — a listing can affect more answers than any single site change and still be the work a business is least able to finish — and reach then orders listings against each other, where the comparison is fair.

The ordering is applied by the query that the page reads, which takes the category sequence as a parameter rather than repeating it as a SQL literal, so the visibility catalog stays its only source. An unknown category sorts last, so a finder added without a catalog entry cannot silently take the top of the queue.

## Persistence and lifecycle

The rebuild migration replaces the six assessment/action tables with `site_audits` and `findings`. This is intentionally destructive because assessments are derived data and the next monitoring run regenerates them.

`site_audits` stores the monitoring run, timestamp, pages read, optional crawl failure, complete check-result JSON, and whether the audit is current. There is one audit per monitoring run and exactly one published audit per business.

`findings` stores one row per `(business_id, key)`. Status is only `OPEN`, `DONE`, or `DISMISSED`:

- a finding is active when it is open and its `last_seen_at` belongs to the current published audit;
- reproducing an open finding refreshes its evidence;
- reproducing a done finding reopens it;
- a dismissed finding remains suppressed when reproduced.

A finding the run does not reproduce is left exactly as it is and simply stops being active, because activity is read from the current audit's timestamp rather than from any per-finding state. There are no retired or superseded states, numbered cycles, event history, completion baselines, or tombstones, and nothing records that a completed finding was later confirmed. Whether work actually changed a business's visibility is a question the product does not answer yet; a re-check of the finding was never an answer to it, and keeping the stamp implied otherwise. Measuring outcomes is a deliberate future design, not a column.

Publication is one transaction: insert the new audit, make it current, and upsert every reproduced finding. The unique monitoring-run constraint makes a retry a no-op. An invalid run/business/account combination also publishes nothing.

## Assessment job and failure posture

One River assessment job is uniquely keyed by the run and performs three coarse operations:

1. `RunSiteAudit` scans the configured website, evaluates every check, and carries the bounded readable site text forward for content-opportunity comparison. Actual same-origin navigation links are read before guessed conventional paths so branded 404 pages cannot displace a real team or services page from the crawl budget.
2. `RunFinders` loads monitoring evidence, executes the finders against one shared research budget of 20 third-party page fetches, then makes one batched source-and-opportunity analysis call on the dedicated Improve model. The page budget is a worst-case bound on serial fetches and SSRF surface, not the ordinary constraint on how much work gets recommended, which the per-candidate cap of two URLs keeps well under it. The budget is per attempt: a retry starts with a full allowance and re-reads pages, because the operation caches nothing.
3. `PublishImproveRun` atomically publishes the audit and findings.

The assessment job has a 30-minute timeout and two attempts. Both crawls fetch serially with every request capped at 20s; `RunSiteAudit` is bounded by its page budget plus robots.txt, and `RunFinders` by its research budget plus headroom for the classification call. A retry re-pays for fetches and model work, while leaving the previous published findings current remains the designed outcome after terminal failure.

Any failure other than the represented crawl failure fails the River job and leaves the prior published state untouched. There is no partial-generation state or stale-scope matrix.

Improve readiness covers the complete chain that can produce a newer publication: monitoring, response analysis, and assessment. During the first chain the Improve pages poll and show that the audit and evidence review are running. On later chains they keep the previous atomically published audit and findings visible with a refresh indicator until the replacement publishes. A terminal assessment failure ends the pending state and preserves the prior publication.

## API and frontend

`ImproveService` exposes:

- `ListActions(business_id)` — the active actions in rank order, resolved actions for direct lookup, the latest check time, an empty reason distinguishing no evidence from not assessed yet, and the categories that currently have active work, each with its label and count, in catalog order;
- `GetAction(action_id)` — one finding translated to the user-facing Action shape;
- `SetActionStatus(action_id, status, dismissal_reason)` — complete, dismiss, or explicitly reopen;
- `GetChecklist(business_id)` — ordered groups, all checks, fixed-order outcome counts, check time, pages read, and crawl failure.

`ListActions` and `GetChecklist` also return `improve_pending`, a product-level readiness flag derived from live River work without exposing queue identifiers, attempts, or errors.

Reads require subscriber access and viewer role. Status changes require member role. Every read and write is account scoped.

The UI has only `/improve/actions` and `/improve/checklist`; `/improve` navigates to actions. Next actions renders concrete finding fields and opens supporting responses in the shared evidence drawer. Active findings remain the page's primary queue; completed and dismissed findings live in one collapsed section so explicit reopening remains reachable without restoring an event timeline. Its empty state says no findings were present in the latest evidence, never that the site is healthy.

A step may carry a preformatted block after a blank line, which the page renders as a copyable code block. That is what makes the generated JSON-LD worth generating: collapsed into the sentence, the indentation and newlines the user has to paste would be lost.

Active actions are one ranked list under no heading of its own, since the page header already names the queue. There is no lead section: a fixed cutoff claimed a priority boundary the ranking does not have, and could push a blocker under a heading that reads as optional.

A card leads with a subdued metadata line — a blocking badge where it applies, the category, and “Seen in N monitored answers” where answer evidence exists — then the title. Deterministic findings retain “Do this”; competitor-derived content findings use “Suggested next step.” Their comparison is headed “What monitored answers said” and “What your site says,” with absence bounded to “Not found in the pages checked.” Quotes use normal, non-italic body text with comfortable line height. The two panels sit side by side only when both contain evidence; otherwise the bounded coverage verdict follows the full-width answer evidence. The copied Markdown preserves the same epistemic language.

The instruction to publish only substantiated facts is a constraint on how every website-content change is written, not a second task, so it is rendered once per content card as a compact inline note beneath the recommendation rather than as a numbered step or boxed callout. It stays on the card rather than moving to a section banner, because the queue can be filtered to one category and a single action can be deep-linked, and neither view may drop it. It is UI copy, not model output: nothing pays a token or a database row to repeat the same sentence on every card.

Inspected pages are listed by host with the full URLs behind a disclosure. A card names the sources it read, and several wrapped absolute URLs bury the one part of them a reader recognizes.

The category is part of that metadata line, and a toggle above the queue filters to one category, held in the URL as `?category=`. The filter appears only when the business has active work in more than one category, offers only categories with active work, applies to the resolved section too, and preserves the ranking. A filtered list takes the category name as its heading. A category in the URL that the business does not currently have is ignored rather than shown as an empty queue, so a stale or shared link still opens.

The checklist always renders all 17 checks in the three stable groups. Group and check titles use everyday language; technical terms appear only where they are needed to make a fix precise. Each row shows an outcome icon, assertion, and label by default. A native per-check disclosure explains what the check means and shows the exact evidence from the latest audit. Counts are absolute, with passed-of-total summaries and no filter, percentage, score, or grade.

## Quality gate and deferred work

Every registered producer must score 100% against at least 15 hand-labelled fixture cases. Fixtures exercise the production functions rather than a parallel scoring implementation.

Deferred finders include curated domain-to-listing instructions, competitor delta, and description accuracy. They extend the finder seam without changing the stable audit catalog.
