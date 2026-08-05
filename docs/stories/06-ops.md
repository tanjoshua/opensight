# Epic 06 — Ops & Checkpoint (OPS)

Backups, the restore drill, and the Phase-1 internal checkpoint. Phase 1 — the restore drill is MVP acceptance, not optional (but it does not gate the checkpoint).

---

## OPS-1 — Backups (restic)

As the operator, I want nightly encrypted offsite backups of both databases and the env file, so that the VPS is not a single point of data loss.

- [ ] Nightly `pg_dump` of `opensight` **and** `temporal` databases (the latter holds all schedules and in-flight state).
- [ ] restic encrypted repository → offsite (Hetzner Storage Box or Backblaze B2); `.env` included in the set.
- [ ] Backup job failure is visible (non-zero exit logged loudly; check documented in ops runbook).

Deps: FND-5 · Phase 1 · Ref: design 07 (Backups and recovery)

## OPS-2 — Restore drill

As the operator, I want a proven restore on a scratch VPS, so that the backup is a backup and not a hope.

- [ ] On a scratch VPS: restore last night's dump, `docker compose up`, app serves, login works, schedules resume in Temporal.
- [ ] Drill steps written down as a runbook (`docs/ops/restore.md`).
- [ ] RPO ≤24h accepted; re-executed week is safe via idempotency keys (verified conceptually against RUN-3).

Deps: OPS-1 · Phase 1 · Ref: design 07 (Backups — restore drill is part of MVP acceptance). Must complete before the MVP is called done; not a gate for OPS-3.

## OPS-3 — Phase-1 checkpoint: end-to-end on production

As the operator, I want a real business running weekly on production with results viewable behind login, so that Phase 1's internal checkpoint is met with real data.

- [ ] Test account + owner membership created (AUTH-2); an internal test business + prompts seeded and schedule created (RUN-5); first run triggered and completed against the real OpenAI API.
- [ ] Logging in shows the run's responses in the Responses section.
- [ ] OpenAI dashboard budget cap confirmed set; cost query (RUN-6) run against the first real week.
- [ ] Backups landing offsite (OPS-1); Sentry receiving from prod; Temporal UI reachable via SSH tunnel; ops runbook covers: check runs weekly, deploy, restore.

Deps: OPS-1, RUN-5, WEB-4, AUTH-4 · Phase 1 · Ref: stories README (Phase 1 checkpoint)
