// Package store holds the Postgres repositories and embedded migrations
// (01-D4, 07).
//
// Multi-tenancy is enforced here, not by Postgres RLS (design 01 D4). Every SQL
// statement that touches a business-owned table (prompts, monitoring_runs,
// prompt_results, profile_proposals) is tenant-scoped one of two ways:
//
//   - it is preceded, in the same transaction, by businessOwned or a
//     tenant-predicated business lock (writes and list methods), or
//   - it carries the JOIN businesses ... tenant_id = $n predicate itself in the
//     same statement (single deep-by-id reads).
//
// Missing and cross-tenant rows are indistinguishable: both return ErrNotFound,
// so the layer is never a cross-tenant existence oracle. The sole
// tenant-unscoped business query is ResolveTenantID, the
// context-establishing bootstrap that callers without ambient tenant context
// (Temporal activities, CLI) use once before switching to the tenant-checked
// repositories.
package store
