-- name: DeleteResultAnalysis :exec
DELETE FROM result_analyses WHERE prompt_result_id=$1 AND prompt_result_id IN (
 SELECT pr.id FROM prompt_results pr JOIN monitoring_runs r ON r.id=pr.run_id
 JOIN businesses b ON b.id=r.business_id WHERE b.account_id = @account_id);

-- name: DeleteResultCitations :exec
DELETE FROM citations WHERE prompt_result_id=$1 AND prompt_result_id IN (
 SELECT pr.id FROM prompt_results pr JOIN monitoring_runs r ON r.id=pr.run_id
 JOIN businesses b ON b.id=r.business_id WHERE b.account_id = @account_id);

-- name: DeleteRunMentions :exec
DELETE FROM mentions WHERE prompt_result_id IN (
 SELECT pr.id FROM prompt_results pr JOIN monitoring_runs r ON r.id=pr.run_id
 JOIN businesses b ON b.id=r.business_id WHERE r.id = @id AND b.account_id = @account_id);

-- name: ResultOwned :one
SELECT 1 FROM prompt_results pr JOIN monitoring_runs r ON r.id=pr.run_id
JOIN businesses b ON b.id=r.business_id WHERE pr.id = @id AND b.account_id = @account_id;

-- name: UpsertResultAnalysis :exec
INSERT INTO result_analyses (prompt_result_id,sentiment,keywords,excerpts,analysis_model,extraction_version,analyzed_at)
VALUES ($1,$2,$3,$4,$5,$6,now())
ON CONFLICT (prompt_result_id) DO UPDATE SET sentiment=EXCLUDED.sentiment,keywords=EXCLUDED.keywords,
 excerpts=EXCLUDED.excerpts,analysis_model=EXCLUDED.analysis_model,
 extraction_version=EXCLUDED.extraction_version,analyzed_at=now();

-- name: DeleteCitationsByResult :exec
DELETE FROM citations WHERE prompt_result_id=$1;

-- name: InsertCitation :exec
INSERT INTO citations (id,prompt_result_id,url,domain,title,cite_order,subject)
VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: ListAnalysisCompetitors :many
SELECT id,name,website,aliases,status FROM competitors
WHERE business_id = @business_id AND business_id IN (SELECT id FROM businesses WHERE account_id = @account_id)
ORDER BY created_at;

-- name: RunBusinessOwned :one
SELECT b.id FROM monitoring_runs r JOIN businesses b ON b.id=r.business_id
WHERE r.id = @id AND b.account_id = @account_id;

-- name: ListSucceededResultIDs :many
SELECT id FROM prompt_results WHERE run_id=$1 AND status='succeeded' ORDER BY requested_at,id;

-- name: RunOwnedByBusiness :one
SELECT 1 FROM monitoring_runs WHERE id=$1 AND business_id=$2;

-- name: InsertDiscoveredCompetitor :exec
INSERT INTO competitors (id,business_id,name,aliases,source,status)
VALUES ($1,$2,$3,ARRAY[$3]::text[],'discovered','discovered');

-- name: AppendSuggestedAlias :exec
UPDATE competitors SET suggested_aliases=array_append(suggested_aliases,$3)
WHERE id=$1 AND business_id=$2 AND NOT ($3=ANY(suggested_aliases)) AND NOT ($3=ANY(aliases));

-- name: InsertMention :exec
INSERT INTO mentions (id,prompt_result_id,subject,competitor_id,matched_by,mention_order,verbatim_name,excerpt)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: SetAnalysisCompleted :exec
UPDATE monitoring_runs SET analysis_completed_at=now() WHERE id=$1 AND business_id=$2;
