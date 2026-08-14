# Epic 16 — Stable site-audit checklist

Design: [09 Improve](../design/09-opportunities.md) · Phase 5

## CHK-1 — Flat check catalog

As a user, I can see the same complete set of site assertions on every visit.

Acceptance: 17 checks with stable key, group, assertion title, methodology, informational flag, optional fix, blocking flag, and priority; groups are presentational only; `Audit` emits exactly one result per catalog check in catalog order.

## CHK-2 — Page facts from one scan

As the product, we read every markup-derived fact during the request and parse already needed for the audit.

Acceptance: title, first heading, meta description, canonical link, telephone links, and bounded JSON-LD are captured per page; robots is read once for all crawler verdicts and sitemap directives; page-fetch and robots failures remain separate; no check adds a second request for a page.

## CHK-3 — Crawler access reported per crawler

As a user, I can tell which OpenAI crawler my site allows and which it does not.

Acceptance: separate OAI-SearchBot, ChatGPT-User, and GPTBot verdicts; GPTBot is informational; an unreadable robots file affects only robots-derived checks; indexing evidence names affected pages; access failures produce blocking findings with concrete steps.

## CHK-4 — Structure and machine-readable identity

As a user, I can see whether crawlers can discover pages and read my business details.

Acceptance: sitemap, robots sitemap declaration, canonical URL, title uniqueness, business name in the homepage title, meta description, structured-data presence and business type, telephone, address, and opening hours; every failed actionable check can produce its own site-audit finding, while opening hours remains checklist-only unless monitored-answer evidence supports a content opportunity.

## CHK-5 — Checklist presentation

As a user, I can inspect everything OpenSight tested without a misleading score.

Acceptance: fixed-order per-outcome counts; passed-of-total summary per group; every check visible with assertion, outcome, and evidence detail; one methodology disclosure per group; informational checks excluded from totals; no filters, percentage, score, or grade; crawl failure and not-yet-assessed states are explicit.

## CHK-6 — Persistence and quality gate

As an operator, I can trust a published audit and every registered evidence producer.

Acceptance: complete audit results persist as one current snapshot with prior audits retained; finding evidence persists with each stable key; every registered producer scores 100% against hand-labelled corpora of at least 15 cases; generated SQL and protobuf clients remain reproducible.
