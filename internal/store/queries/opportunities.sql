-- name: UpsertAssessmentGeneration :one
INSERT INTO assessment_generations (id,account_id,business_id,monitoring_run_id,status,compiler_version,ranker_version,module_plan)
SELECT @id,@account_id,@business_id,@monitoring_run_id,'RUNNING',@compiler_version,@ranker_version,@module_plan
WHERE EXISTS (SELECT 1 FROM monitoring_runs r JOIN businesses b ON b.id=r.business_id
 WHERE r.id = sqlc.arg('monitoring_run_id') AND r.business_id = sqlc.arg('business_id') AND b.account_id = sqlc.arg('account_id') AND r.analysis_completed_at IS NOT NULL)
ON CONFLICT (monitoring_run_id) DO UPDATE SET module_plan=assessment_generations.module_plan
RETURNING id,status;

-- name: UpsertEvidenceArtifact :exec
INSERT INTO evidence_artifacts (id,generation_id,account_id,collector_key,collector_version,payload_version,status,checked_at,payload,error)
SELECT @id,@generation_id,@account_id,@collector_key,@collector_version,@payload_version,@status,@checked_at,@payload,@error
WHERE EXISTS (SELECT 1 FROM assessment_generations WHERE id = sqlc.arg('generation_id') AND account_id = sqlc.arg('account_id'))
ON CONFLICT (generation_id,collector_key) DO UPDATE SET collector_version=EXCLUDED.collector_version,
 payload_version=EXCLUDED.payload_version,status=EXCLUDED.status,checked_at=EXCLUDED.checked_at,payload=EXCLUDED.payload,error=EXCLUDED.error;

-- name: UpsertVisibilityAssessment :one
INSERT INTO visibility_assessments (id,generation_id,account_id,business_id,practice_key,criteria_version,
 assessor_key,assessor_version,subject_key,status,result_ids,prompt_ids,checked_sources,
 explanation,reach,persistence,evidence_quality,actionability,effort,payload_version,payload)
SELECT @id,@generation_id,@account_id,@business_id,@practice_key,@criteria_version,@assessor_key,
 @assessor_version,@subject_key,@status,@result_ids,@prompt_ids,@checked_sources,
 @explanation,@reach,@persistence,@evidence_quality,@actionability,@effort,@payload_version,@payload
WHERE EXISTS (SELECT 1 FROM assessment_generations WHERE id = sqlc.arg('generation_id') AND account_id = sqlc.arg('account_id') AND business_id = sqlc.arg('business_id'))
ON CONFLICT (generation_id,practice_key,subject_key) DO UPDATE SET status=EXCLUDED.status,
 result_ids=EXCLUDED.result_ids,prompt_ids=EXCLUDED.prompt_ids,checked_sources=EXCLUDED.checked_sources,
 explanation=EXCLUDED.explanation,reach=EXCLUDED.reach,
 persistence=EXCLUDED.persistence,evidence_quality=EXCLUDED.evidence_quality,actionability=EXCLUDED.actionability,
 effort=EXCLUDED.effort,payload=EXCLUDED.payload
RETURNING id;

-- name: UpsertOpportunity :one
INSERT INTO opportunities (id,account_id,business_id,practice_key,subject_key,current_assessment_id,current_generation_id,rank,presentation)
SELECT @id,@account_id,@business_id,@practice_key,@subject_key,@current_assessment_id,sqlc.arg('current_generation_id')::uuid,@rank,@presentation
WHERE EXISTS (SELECT 1 FROM visibility_assessments WHERE id = sqlc.arg('current_assessment_id') AND account_id = sqlc.arg('account_id') AND business_id = sqlc.arg('business_id'))
ON CONFLICT (business_id,practice_key,subject_key) DO UPDATE SET current_assessment_id=EXCLUDED.current_assessment_id,
 current_generation_id=EXCLUDED.current_generation_id,rank=EXCLUDED.rank,presentation=EXCLUDED.presentation,updated_at=now()
RETURNING id;

-- Everything the compiler did not produce this generation stops being current.
-- The last-known presentation stays on the row so acted-on history still renders.
-- name: SweepStaleOpportunities :exec
UPDATE opportunities SET current_generation_id = NULL, updated_at = now()
WHERE business_id = sqlc.arg('business_id') AND account_id = sqlc.arg('account_id')
 AND current_generation_id IS NOT NULL AND current_generation_id <> sqlc.arg('current_generation_id')::uuid;

-- name: FinishAssessmentGeneration :exec
UPDATE assessment_generations SET status = sqlc.arg('status'), error = sqlc.narg('error'), completed_at = now()
WHERE id = sqlc.arg('id') AND account_id = sqlc.arg('account_id');

-- A row that stopped being current and that the user never acted on is not
-- history — it simply disappears.
-- name: ListCurrentOpportunities :many
SELECT o.id,o.practice_key,o.subject_key,o.rank,o.presentation,o.user_status,o.dismissal_reason,
 o.completion_baseline,o.first_seen_at,o.updated_at,(o.current_generation_id IS NOT NULL)::bool AS current,
 COALESCE(f.focus,false)::bool AS focus,a.status AS assessment_status,
 a.result_ids,a.prompt_ids,a.checked_sources,a.explanation,a.assessed_at
FROM opportunities o JOIN visibility_assessments a ON a.id=o.current_assessment_id
 LEFT JOIN opportunity_focus f ON f.id=o.id
WHERE o.business_id = sqlc.arg('business_id') AND o.account_id = sqlc.arg('account_id')
 AND (o.current_generation_id IS NOT NULL OR o.user_status <> 'OPEN')
ORDER BY o.rank,o.practice_key,o.subject_key;

-- Outcome observations match this generation's assessment by practice identity,
-- not by the opportunity's current assessment pointer, so a completed item that
-- has since turned MET still receives its later observation.
-- name: ListCompletedGenerationOpportunities :many
SELECT o.id,o.practice_key,o.subject_key,a.status AS assessment_status,a.result_ids,a.prompt_ids
FROM opportunities o JOIN visibility_assessments a
 ON a.business_id=o.business_id AND a.practice_key=o.practice_key AND a.subject_key=o.subject_key
WHERE o.account_id = sqlc.arg('account_id') AND o.business_id = sqlc.arg('business_id') AND o.user_status='COMPLETED'
 AND a.generation_id = sqlc.arg('generation_id') ORDER BY o.id;

-- name: GetCurrentOpportunity :one
SELECT o.id,o.business_id,o.practice_key,o.subject_key,o.rank,o.presentation,o.user_status,o.dismissal_reason,
 o.completion_baseline,o.first_seen_at,o.updated_at,(o.current_generation_id IS NOT NULL)::bool AS current,
 COALESCE(f.focus,false)::bool AS focus,a.status AS assessment_status,
 a.result_ids,a.prompt_ids,a.checked_sources,a.explanation,a.assessed_at
FROM opportunities o JOIN visibility_assessments a ON a.id=o.current_assessment_id
 LEFT JOIN opportunity_focus f ON f.id=o.id
WHERE o.id = sqlc.arg('id') AND o.account_id = sqlc.arg('account_id');

-- name: SetOpportunityStatus :one
UPDATE opportunities SET user_status = sqlc.arg('user_status'), dismissal_reason = sqlc.narg('dismissal_reason'),
 completion_baseline = CASE WHEN sqlc.arg('user_status') = 'COMPLETED' THEN COALESCE(completion_baseline, sqlc.narg('completion_baseline')) ELSE completion_baseline END,
 updated_at = now() WHERE id = sqlc.arg('id') AND account_id = sqlc.arg('account_id')
RETURNING business_id,practice_key,subject_key;

-- name: InsertOpportunityEvent :exec
INSERT INTO opportunity_events (id,opportunity_id,account_id,event_key,event_type,payload)
SELECT @id,@opportunity_id,@account_id,@event_key,@event_type,@payload
WHERE EXISTS (SELECT 1 FROM opportunities WHERE id = sqlc.arg('opportunity_id') AND account_id = sqlc.arg('account_id'))
ON CONFLICT (opportunity_id,event_key) DO NOTHING;

-- name: ListOpportunityOutcomeEvents :many
SELECT payload,created_at FROM opportunity_events
WHERE opportunity_id = sqlc.arg('opportunity_id') AND account_id = sqlc.arg('account_id') AND event_type='OUTCOME_OBSERVED'
ORDER BY created_at,event_key;

-- name: LoadMonitoringEvidence :many
SELECT r.id AS run_id,pr.id AS result_id,pr.prompt_id,p.text AS prompt,pr.response_text,
 EXISTS(SELECT 1 FROM mentions m WHERE m.prompt_result_id=pr.id AND m.subject='self') AS mentioned,
 COALESCE((SELECT array_agg(DISTINCT c.domain ORDER BY c.domain) FROM citations c WHERE c.prompt_result_id=pr.id),'{}')::text[] AS citation_domains,
 COALESCE((SELECT array_agg(c.url ORDER BY c.cite_order,c.id) FROM citations c WHERE c.prompt_result_id=pr.id),'{}')::text[] AS citation_urls,
 COALESCE((SELECT array_agg(DISTINCT co.name ORDER BY co.name) FROM mentions m JOIN competitors co ON co.id=m.competitor_id WHERE m.prompt_result_id=pr.id),'{}')::text[] AS competitors
FROM monitoring_runs r JOIN prompt_results pr ON pr.run_id=r.id JOIN prompts p ON p.id=pr.prompt_id
JOIN businesses b ON b.id=r.business_id
WHERE r.business_id = sqlc.arg('business_id') AND b.account_id = sqlc.arg('account_id') AND r.analysis_completed_at IS NOT NULL
 AND r.id IN (SELECT id FROM monitoring_runs WHERE business_id = sqlc.arg('business_id') AND analysis_completed_at IS NOT NULL ORDER BY scheduled_for DESC LIMIT 4)
 AND pr.status='succeeded' ORDER BY r.scheduled_for DESC,pr.requested_at,pr.id;
