-- name: UpsertAssessmentGeneration :one
INSERT INTO assessment_generations (id,account_id,business_id,monitoring_run_id,status,compiler_version,ranker_version,module_plan)
SELECT @id,@account_id,@business_id,@monitoring_run_id,'RUNNING',@compiler_version,@ranker_version,@module_plan
WHERE EXISTS (SELECT 1 FROM monitoring_runs r JOIN businesses b ON b.id=r.business_id
 WHERE r.id=sqlc.arg('monitoring_run_id') AND r.business_id=sqlc.arg('business_id') AND b.account_id=sqlc.arg('account_id') AND r.analysis_completed_at IS NOT NULL)
ON CONFLICT (monitoring_run_id) DO UPDATE SET module_plan=assessment_generations.module_plan
RETURNING id,status;

-- name: UpsertEvidenceArtifact :exec
INSERT INTO evidence_artifacts (id,generation_id,account_id,collector_key,collector_version,payload_version,status,checked_at,payload,error)
SELECT @id,@generation_id,@account_id,@collector_key,@collector_version,@payload_version,@status,@checked_at,@payload,@error
WHERE EXISTS (SELECT 1 FROM assessment_generations WHERE id=sqlc.arg('generation_id') AND account_id=sqlc.arg('account_id'))
ON CONFLICT (generation_id,collector_key) DO UPDATE SET collector_version=EXCLUDED.collector_version,payload_version=EXCLUDED.payload_version,status=EXCLUDED.status,checked_at=EXCLUDED.checked_at,payload=EXCLUDED.payload,error=EXCLUDED.error;

-- name: UpsertVisibilityAssessment :one
INSERT INTO visibility_assessments (id,generation_id,account_id,business_id,practice_key,criteria_version,assessor_key,assessor_version,subject_key,status,result_ids,prompt_ids,checked_sources,explanation,reach,persistence,evidence_quality,actionability,effort,payload_version,payload)
SELECT @id,@generation_id,@account_id,@business_id,@practice_key,@criteria_version,@assessor_key,@assessor_version,@subject_key,@status,@result_ids,@prompt_ids,@checked_sources,@explanation,@reach,@persistence,@evidence_quality,@actionability,@effort,@payload_version,@payload
WHERE EXISTS (SELECT 1 FROM assessment_generations WHERE id=sqlc.arg('generation_id') AND account_id=sqlc.arg('account_id') AND business_id=sqlc.arg('business_id'))
ON CONFLICT (generation_id,practice_key,subject_key) DO UPDATE SET status=EXCLUDED.status,result_ids=EXCLUDED.result_ids,prompt_ids=EXCLUDED.prompt_ids,checked_sources=EXCLUDED.checked_sources,explanation=EXCLUDED.explanation,reach=EXCLUDED.reach,persistence=EXCLUDED.persistence,evidence_quality=EXCLUDED.evidence_quality,actionability=EXCLUDED.actionability,effort=EXCLUDED.effort,payload=EXCLUDED.payload
RETURNING id;

-- name: UpsertModuleOutcome :exec
INSERT INTO assessment_module_outcomes (generation_id,account_id,assessor_key,status,error)
VALUES (@generation_id,@account_id,@assessor_key,@status,@error)
ON CONFLICT (generation_id,assessor_key) DO UPDATE SET status=EXCLUDED.status,error=EXCLUDED.error,completed_at=now();

-- name: LockAssessmentGeneration :one
SELECT status FROM assessment_generations WHERE id = @id AND account_id = @account_id FOR UPDATE;

-- name: ListPublishedForPractices :many
SELECT * FROM visibility_assessments
WHERE business_id = @business_id AND account_id = @account_id AND published AND practice_key = ANY(@practice_keys::text[])
ORDER BY practice_key,subject_key;

-- name: UnpublishPractices :exec
UPDATE visibility_assessments SET published=false
WHERE business_id = @business_id AND account_id = @account_id AND published AND practice_key = ANY(@practice_keys::text[]);

-- name: PublishGenerationPractices :exec
UPDATE visibility_assessments SET published=true
WHERE generation_id = @generation_id AND account_id = @account_id AND practice_key = ANY(@practice_keys::text[]);

-- name: ListGenerationAssessmentRows :many
SELECT * FROM visibility_assessments WHERE generation_id = @generation_id AND account_id = @account_id ORDER BY practice_key,subject_key;

-- name: ListActiveActionsForPractices :many
SELECT * FROM improvement_actions
WHERE business_id = @business_id AND account_id = @account_id AND practice_key = ANY(@practice_keys::text[]) AND status IN ('OPEN','IN_PROGRESS')
ORDER BY rank,practice_key,subject_key;

-- name: InsertImprovementAction :one
INSERT INTO improvement_actions (id,account_id,business_id,practice_key,subject_key,cycle,recommendation_key,current_assessment_id,rank,presentation,status)
VALUES (@id,@account_id,@business_id,@practice_key,@subject_key,@cycle,@recommendation_key,@current_assessment_id,@rank,@presentation,'OPEN') RETURNING *;

-- name: UpdateActiveImprovementAction :exec
UPDATE improvement_actions SET current_assessment_id = @current_assessment_id,rank = @rank,presentation = @presentation,updated_at = now()
WHERE id = @id AND account_id = @account_id AND status IN ('OPEN','IN_PROGRESS');

-- name: EndImprovementAction :exec
UPDATE improvement_actions SET status = @status,current_assessment_id = @current_assessment_id,updated_at = now()
WHERE id = @id AND account_id = @account_id AND status IN ('OPEN','IN_PROGRESS');

-- name: InsertImprovementActionEvent :exec
INSERT INTO improvement_action_events (id,action_id,account_id,event_key,event_type,payload)
SELECT @id,@action_id,@account_id,@event_key,@event_type,@payload
WHERE EXISTS (SELECT 1 FROM improvement_actions WHERE id = @action_id AND account_id = @account_id)
ON CONFLICT (action_id,event_key) DO NOTHING;

-- name: FinishAssessmentGeneration :exec
UPDATE assessment_generations SET status = @status,error = sqlc.narg('error'),completed_at = now() WHERE id = @id AND account_id = @account_id;

-- name: FailAssessmentGeneration :exec
UPDATE assessment_generations SET status = 'FAILED',error = @error,completed_at = now() WHERE id = @id AND account_id = @account_id AND status = 'RUNNING';

-- name: GetLatestPublishedGeneration :one
SELECT g.* FROM assessment_generations g WHERE g.business_id = @business_id AND g.account_id = @account_id AND g.status IN ('READY','PARTIAL') ORDER BY g.completed_at DESC LIMIT 1;

-- name: ListPublishedAssessments :many
SELECT a.*,g.status AS generation_status,g.completed_at AS generation_completed_at
FROM visibility_assessments a JOIN assessment_generations g ON g.id=a.generation_id
WHERE a.business_id = @business_id AND a.account_id = @account_id AND a.published
ORDER BY a.practice_key,a.subject_key;

-- name: ListBusinessActions :many
SELECT a.*,v.status AS assessment_status,v.result_ids,v.prompt_ids,v.checked_sources,v.explanation,v.assessed_at,
 (v.generation_id=(SELECT id FROM assessment_generations WHERE business_id=a.business_id AND account_id=a.account_id AND status IN ('READY','PARTIAL') ORDER BY completed_at DESC LIMIT 1))::bool AS fresh
FROM improvement_actions a LEFT JOIN visibility_assessments v ON v.id=a.current_assessment_id
WHERE a.business_id = @business_id AND a.account_id = @account_id
ORDER BY a.rank,a.practice_key,a.subject_key,a.cycle DESC;

-- name: GetBusinessAction :one
SELECT a.*,v.status AS assessment_status,v.result_ids,v.prompt_ids,v.checked_sources,v.explanation,v.assessed_at,
 (v.generation_id=(SELECT id FROM assessment_generations WHERE business_id=a.business_id AND account_id=a.account_id AND status IN ('READY','PARTIAL') ORDER BY completed_at DESC LIMIT 1))::bool AS fresh
FROM improvement_actions a LEFT JOIN visibility_assessments v ON v.id=a.current_assessment_id
WHERE a.id = @id AND a.account_id = @account_id;

-- name: ListActionCycles :many
SELECT * FROM improvement_actions WHERE business_id = @business_id AND account_id = @account_id AND practice_key = @practice_key AND subject_key = @subject_key ORDER BY cycle DESC;

-- name: SetImprovementActionStatus :one
UPDATE improvement_actions SET status = @status,dismissal_reason = sqlc.narg('dismissal_reason'),
 started_at=CASE WHEN @status='IN_PROGRESS' THEN COALESCE(started_at,now()) ELSE started_at END,
 completed_at=CASE WHEN @status='COMPLETED' THEN COALESCE(completed_at,now()) ELSE completed_at END,
 completion_baseline=CASE WHEN @status='COMPLETED' THEN COALESCE(completion_baseline,sqlc.narg('completion_baseline')) ELSE completion_baseline END,
 updated_at = now() WHERE id = @id AND account_id = @account_id AND status = @previous_status RETURNING *;

-- name: LoadCompletionQuestionEvidence :many
SELECT DISTINCT ON (pr.prompt_id) pr.prompt_id,p.text AS prompt,pr.id AS result_id,
 EXISTS (SELECT 1 FROM mentions m WHERE m.prompt_result_id=pr.id AND m.subject='self') AS mentioned
FROM prompt_results pr JOIN result_analyses ra ON ra.prompt_result_id=pr.id JOIN prompts p ON p.id=pr.prompt_id JOIN monitoring_runs r ON r.id=pr.run_id JOIN businesses b ON b.id=r.business_id
WHERE r.business_id = @business_id AND b.account_id = @account_id AND pr.status='succeeded'
 AND (pr.id = ANY(@result_ids::uuid[]) OR pr.prompt_id = ANY(@prompt_ids::uuid[]))
ORDER BY pr.prompt_id,(pr.id = ANY(@result_ids::uuid[])) DESC,pr.requested_at DESC;

-- name: LoadCompletionBusinessTotals :one
SELECT count(*) FILTER (WHERE EXISTS (SELECT 1 FROM mentions m WHERE m.prompt_result_id=pr.id AND m.subject='self'))::int AS mentioned,
 count(*)::int AS analyzed
FROM prompt_results pr JOIN result_analyses ra ON ra.prompt_result_id=pr.id JOIN monitoring_runs r ON r.id=pr.run_id JOIN businesses b ON b.id=r.business_id
WHERE r.business_id = @business_id AND b.account_id = @account_id AND r.analysis_completed_at IS NOT NULL AND pr.status='succeeded'
 AND r.id = (SELECT id FROM monitoring_runs WHERE business_id = @business_id AND analysis_completed_at IS NOT NULL ORDER BY scheduled_for DESC LIMIT 1);

-- name: ListActionEvents :many
SELECT e.*,a.business_id,a.practice_key,a.subject_key,a.cycle,a.presentation
FROM improvement_action_events e JOIN improvement_actions a ON a.id=e.action_id
WHERE a.business_id = @business_id AND e.account_id = @account_id
ORDER BY e.created_at DESC,e.id DESC LIMIT @page_limit OFFSET @page_offset;

-- name: CountActionEvents :one
SELECT count(*) FROM improvement_action_events e JOIN improvement_actions a ON a.id=e.action_id
WHERE a.business_id = @business_id AND e.account_id = @account_id;

-- name: LoadMonitoringEvidence :many
SELECT r.id AS run_id,pr.id AS result_id,pr.prompt_id,p.text AS prompt,pr.response_text,
 EXISTS(SELECT 1 FROM mentions m WHERE m.prompt_result_id=pr.id AND m.subject='self') AS mentioned,
 COALESCE((SELECT array_agg(DISTINCT c.domain ORDER BY c.domain) FROM citations c WHERE c.prompt_result_id=pr.id),'{}')::text[] AS citation_domains,
 COALESCE((SELECT array_agg(c.url ORDER BY c.cite_order,c.id) FROM citations c WHERE c.prompt_result_id=pr.id),'{}')::text[] AS citation_urls,
 COALESCE((SELECT array_agg(DISTINCT co.name ORDER BY co.name) FROM mentions m JOIN competitors co ON co.id=m.competitor_id WHERE m.prompt_result_id=pr.id),'{}')::text[] AS competitors
FROM monitoring_runs r JOIN prompt_results pr ON pr.run_id=r.id JOIN prompts p ON p.id=pr.prompt_id JOIN businesses b ON b.id=r.business_id
WHERE r.business_id = @business_id AND b.account_id = @account_id AND r.analysis_completed_at IS NOT NULL
 AND r.id IN (SELECT id FROM monitoring_runs WHERE business_id = @business_id AND analysis_completed_at IS NOT NULL ORDER BY scheduled_for DESC LIMIT 4)
 AND pr.status='succeeded' ORDER BY r.scheduled_for DESC,pr.requested_at,pr.id;
