// Package metrics is the single place every product metric is computed
// (design 06: "All metrics are computed server-side in one shared
// internal/metrics package"). Overview, Prompts, and Competitors call into it so
// they can never disagree on what visibility means.
//
// Two invariants, enforced here for every metric (design 02/05):
//
//   - Mention facts come only from the mentions table; result_analyses never
//     answers "was X mentioned".
//   - A result enters the metrics base only when its run has
//     analysis_completed_at set AND the result has a result_analyses row.
//     Succeeded-but-unanalyzed results and results of not-yet-reconciled runs
//     are excluded from numerator and denominator alike — an analysis failure
//     shows as a badge (06), never as a visibility drop.
//
// Every aggregate carries the result_ids behind it — the "every number is a
// door" contract (design 06): each figure the API returns links back to the
// exact responses it was computed from.
//
// Queries are read-only and defensively tenant-scoped through the businesses
// join (b.id = $1 AND b.tenant_id = $2); a business the tenant does not own
// yields empty results rather than leaking another tenant's rows. Ownership
// itself is validated by the HTTP layer before these methods are called
// (design 06).
package metrics
