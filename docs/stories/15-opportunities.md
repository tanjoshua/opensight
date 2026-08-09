# Epic 15 — Improve findings and lifecycle

Design: [09 Improve](../design/09-opportunities.md) · Phase 5

## IMP-1 — Separate audit and finding truth

As a user, I can distinguish the stable record of what OpenSight tested from changing evidence-derived advice.

Acceptance: one deterministic site audit with exactly one result per catalog check; open-ended finders consume the audit and latest monitoring snapshot; readable finding keys; a crawl failure publishes all checks as unverifiable; no practice, assessor, collector, generation, or presentation abstraction remains.

## IMP-2 — Atomic publication

As an operator, I can retry an Improve run without duplicating or partially replacing current state.

Acceptance: one transaction publishes the audit and findings; one audit per monitoring run and one current audit per business; a repeated run is a no-op; finder or publication failure leaves prior state current; every query is account scoped.

## IMP-3 — Trustworthy citation opportunities

As a user, I receive a listing action only for an independently operated source, while a competitor's own source becomes useful evidence for improving my site.

Acceptance: recurrence and reach count only distinct answers where the business is absent and the citation is linked to a competitor; exact stored citation passages and inspected pages feed one validated batched ownership-classification call per assessment; `third_party` and `unknown` remain listing actions; `competitor_owned` produces `competitor-content:<domain>` in Business details with at most three exact claims and safe own-site publishing guidance; classification failure publishes nothing.

## IMP-4 — Next actions

As a user, I can focus on the most relevant work supported by current evidence.

Acceptance: blockers first, then affected-question reach, priority, and stable key; three focus actions followed by all additional active findings; concrete explanation, steps, checked sources, and response evidence; complete, dismiss, and reopen controls; no-findings copy makes no health claim.

## IMP-5 — Simple lifecycle and verification

As a user, I can act on or suppress a finding without maintaining artificial action cycles.

Acceptance: `OPEN`, `DONE`, and `DISMISSED` only; a reproduced done finding reopens; a dismissed finding never reopens automatically; an open finding not reproduced by the current audit leaves the queue; the first later run not reproducing a done finding records confirmation without claiming a visibility outcome.

## IMP-6 — Selection criteria from unattributed citations

As a user, I can see which credential or capability an answer selects on, and state it on my own site.

Acceptance: a `selection-criteria` finder reads the cited sources `citation-gap` drops for zero competitor lift, and that drop is unchanged; a candidate is a cited sentence whose citation attributes to no business, kept only when the source recurs across questions or runs; each finding names the criterion, the responses it was read from, and steps to make that credential or capability visible and machine-readable on the business's own site; category is `identity` or `structure` and never `listings`, and no step asks the user to get listed on, apply to, or contact an accreditation body or regulator — guarded by test; registered with the fixture quality gate.
