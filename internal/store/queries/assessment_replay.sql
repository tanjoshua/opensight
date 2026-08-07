-- Read-only catalog for `opensight assess replay`. Nothing here writes: replay
-- re-derives verdicts from evidence that was already captured and compares them
-- against what was stored, so the historical record is never touched.

-- name: ListReplayGenerations :many
SELECT g.id, g.account_id, g.business_id, g.monitoring_run_id, g.status, g.started_at
FROM assessment_generations g
WHERE (sqlc.narg('business_id')::uuid IS NULL OR g.business_id = sqlc.narg('business_id')::uuid)
  AND (sqlc.narg('since')::timestamptz IS NULL OR g.started_at >= sqlc.narg('since')::timestamptz)
  AND (sqlc.narg('until')::timestamptz IS NULL OR g.started_at < sqlc.narg('until')::timestamptz)
ORDER BY g.started_at, g.id
LIMIT sqlc.arg('max_generations')::int;

-- Only SUCCEEDED artifacts are replayable: a FAILED row stores a placeholder
-- payload, and feeding that to an assessor would manufacture a verdict from
-- evidence that was never collected.
-- name: ListGenerationEvidence :many
SELECT collector_key, collector_version, payload_version, checked_at, payload
FROM evidence_artifacts
WHERE generation_id = sqlc.arg('generation_id') AND status = 'SUCCEEDED'
ORDER BY collector_key;

-- name: ListGenerationAssessments :many
SELECT practice_key, subject_key, status
FROM visibility_assessments
WHERE generation_id = sqlc.arg('generation_id')
ORDER BY practice_key, subject_key;
