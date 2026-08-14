-- name: InsertRunOnConflictNothing :exec
INSERT INTO monitoring_runs (id,business_id,platform,trigger,scheduled_for,status,job_id,expected_results,spec)
VALUES ($1,$2,$3,$4,$5,'running',$6,$7,$8)
ON CONFLICT (business_id,platform,scheduled_for) DO NOTHING;

-- name: SelectRunByKey :one
SELECT id,business_id,platform,trigger,scheduled_for,status,job_id,started_at,completed_at,analysis_completed_at,expected_results,spec
FROM monitoring_runs WHERE business_id=$1 AND platform=$2 AND scheduled_for=$3;

-- name: GetRunByKey :one
SELECT r.id,r.business_id,r.platform,r.trigger,r.scheduled_for,r.status,r.job_id,r.started_at,
 r.completed_at,r.analysis_completed_at,r.expected_results,r.spec
FROM monitoring_runs r JOIN businesses b ON b.id=r.business_id
WHERE r.business_id=$1 AND r.platform=$2 AND r.scheduled_for=$3 AND b.account_id=$4;

-- name: MonitoringRunExists :one
SELECT EXISTS (
  SELECT 1 FROM monitoring_runs
  WHERE business_id=$1 AND platform=$2 AND scheduled_for=$3
);

-- name: FinalizeRun :one
UPDATE monitoring_runs r
SET status=CASE WHEN sub.succeeded=0 THEN 'failed'
  WHEN sub.succeeded=COALESCE(r.expected_results,sub.total) THEN 'completed' ELSE 'partial' END,
  completed_at=now()
FROM businesses b,(SELECT count(*) FILTER (WHERE status='succeeded') AS succeeded,count(*) AS total
  FROM prompt_results WHERE run_id = @id) sub
WHERE r.id = @id AND r.business_id=b.id AND b.account_id = @account_id
RETURNING r.id,r.business_id,r.platform,r.trigger,r.scheduled_for,r.status,r.job_id,
 r.started_at,r.completed_at,r.analysis_completed_at,r.expected_results;

-- name: ListRuns :many
SELECT r.id,r.business_id,r.platform,r.trigger,r.scheduled_for,r.status,r.job_id,
 r.started_at,r.completed_at,r.analysis_completed_at,r.expected_results,
 c.succeeded,c.failed,c.analyzed
FROM monitoring_runs r
LEFT JOIN LATERAL (
 SELECT count(*) FILTER (WHERE pr.status='succeeded') AS succeeded,
 count(*) FILTER (WHERE pr.status='failed') AS failed,count(ra.prompt_result_id) AS analyzed
 FROM prompt_results pr LEFT JOIN result_analyses ra ON ra.prompt_result_id=pr.id
 WHERE pr.run_id=r.id
) c ON true WHERE r.business_id=$1 ORDER BY r.scheduled_for DESC;

-- name: RunPromptOwned :one
SELECT 1 FROM monitoring_runs r
JOIN businesses b ON b.id=r.business_id
JOIN prompts p ON p.id = @prompt_id AND p.business_id=r.business_id
WHERE r.id = @id AND b.account_id = @account_id;

-- name: InsertResult :one
INSERT INTO prompt_results (id,run_id,prompt_id,status,model,request,raw_response,response_text,error,requested_at,completed_at)
VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,
  COALESCE(sqlc.narg('requested_at')::timestamptz, now()),
  COALESCE(sqlc.narg('completed_at')::timestamptz, now())
)
RETURNING requested_at,completed_at;

-- name: GetResultByRunAndPrompt :one
SELECT pr.id,pr.run_id,pr.prompt_id,pr.status,pr.model,pr.request,pr.raw_response,pr.response_text,
 pr.error,pr.requested_at,pr.completed_at
FROM prompt_results pr JOIN monitoring_runs r ON r.id=pr.run_id JOIN businesses b ON b.id=r.business_id
WHERE pr.run_id = @run_id AND pr.prompt_id = @prompt_id AND b.account_id = @account_id;

-- name: GetResult :one
SELECT pr.id,pr.run_id,pr.prompt_id,pr.status,pr.model,pr.request,pr.raw_response,pr.response_text,
 pr.error,pr.requested_at,pr.completed_at
FROM prompt_results pr JOIN monitoring_runs r ON r.id=pr.run_id JOIN businesses b ON b.id=r.business_id
WHERE pr.id = @id AND b.account_id = @account_id;

-- name: GetResultDetail :one
SELECT pr.id,pr.run_id,pr.prompt_id,pr.status,pr.model,pr.request,pr.raw_response,pr.response_text,
 pr.error,pr.requested_at,pr.completed_at,p.text,r.business_id,r.platform,r.trigger,r.scheduled_for,
 r.status AS run_status,r.job_id,r.started_at,r.completed_at AS run_completed_at,r.analysis_completed_at
FROM prompt_results pr JOIN monitoring_runs r ON r.id=pr.run_id JOIN businesses b ON b.id=r.business_id
JOIN prompts p ON p.id=pr.prompt_id AND p.business_id=r.business_id
WHERE pr.id = @id AND b.account_id = @account_id;

-- name: GetResultAnalysisRow :one
SELECT ra.sentiment,ra.keywords,ra.excerpts FROM result_analyses ra
JOIN prompt_results pr ON pr.id=ra.prompt_result_id JOIN monitoring_runs r ON r.id=pr.run_id
JOIN businesses b ON b.id=r.business_id WHERE ra.prompt_result_id = @prompt_result_id AND b.account_id = @account_id;

-- name: ListResultMentions :many
SELECT m.subject,COALESCE(m.verbatim_name,m.excerpt)::text AS verbatim_name,m.matched_by,m.mention_order,m.excerpt
FROM mentions m JOIN prompt_results pr ON pr.id=m.prompt_result_id
JOIN monitoring_runs r ON r.id=pr.run_id JOIN businesses b ON b.id=r.business_id
WHERE m.prompt_result_id = @prompt_result_id AND b.account_id = @account_id ORDER BY m.mention_order,m.id;

-- name: ListResultCitations :many
SELECT c.url,c.domain,c.title,c.cite_order,c.subject FROM citations c
JOIN prompt_results pr ON pr.id=c.prompt_result_id JOIN monitoring_runs r ON r.id=pr.run_id
JOIN businesses b ON b.id=r.business_id WHERE c.prompt_result_id = @prompt_result_id AND b.account_id = @account_id
ORDER BY c.cite_order,c.id;

-- name: ListResults :many
SELECT pr.id,pr.run_id,pr.prompt_id,pr.status,pr.model,pr.request,pr.raw_response,pr.response_text,
 pr.error,pr.requested_at,pr.completed_at,p.text AS prompt_text,
 EXISTS(SELECT 1 FROM result_analyses ra WHERE ra.prompt_result_id=pr.id) AS analyzed,
 EXISTS(SELECT 1 FROM mentions m WHERE m.prompt_result_id=pr.id AND m.subject='self') AS self_mentioned
FROM prompt_results pr JOIN monitoring_runs r ON r.id=pr.run_id
JOIN prompts p ON p.id=pr.prompt_id AND p.business_id=r.business_id
WHERE r.business_id = @business_id
 AND (sqlc.narg('run_id')::uuid IS NULL OR pr.run_id=sqlc.narg('run_id'))
 AND (sqlc.narg('prompt_id')::uuid IS NULL OR pr.prompt_id=sqlc.narg('prompt_id'))
 AND (sqlc.narg('status')::text IS NULL OR pr.status=sqlc.narg('status'))
 AND (sqlc.narg('mentioned')::bool IS NULL OR EXISTS(
   SELECT 1 FROM mentions m WHERE m.prompt_result_id=pr.id AND m.subject='self')=sqlc.narg('mentioned'))
 AND (coalesce(cardinality(@result_ids::uuid[]), 0)=0 OR pr.id=ANY(@result_ids::uuid[]))
ORDER BY CASE WHEN coalesce(cardinality(@result_ids::uuid[]), 0)>0 THEN array_position(@result_ids::uuid[],pr.id) END,
 pr.requested_at DESC,pr.id
LIMIT @result_limit OFFSET @result_offset;
