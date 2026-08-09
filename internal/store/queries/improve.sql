-- name: InsertSiteAudit :one
-- The unique monitoring_run_id makes a retried publish a no-op rather than a
-- second audit, which is the whole idempotency story now that there is no
-- generation state machine.
-- The run is joined rather than passed straight through, so the insert only
-- happens for a run that really belongs to this business and account.
INSERT INTO site_audits (id,account_id,business_id,monitoring_run_id,checked_at,pages_read,failure,checks,published)
SELECT @id,@account_id,@business_id,r.id,now(),@pages_read,sqlc.narg('failure'),@checks,false
FROM monitoring_runs r JOIN businesses b ON b.id=r.business_id
WHERE r.id = @monitoring_run_id AND r.business_id = @business_id AND b.account_id = @account_id
ON CONFLICT (monitoring_run_id) DO NOTHING
RETURNING id;

-- name: PublishSiteAudit :exec
-- Unpublish-then-publish under one transaction, guarded by the partial unique
-- index, so exactly one audit is ever current for a business.
UPDATE site_audits SET published = (id = @id) WHERE business_id = @business_id AND (published OR id = @id);

-- name: GetPublishedSiteAudit :one
SELECT * FROM site_audits WHERE business_id = @business_id AND account_id = @account_id AND published;

-- name: UpsertFinding :exec
-- A finder reproducing a key refreshes the evidence and moves last_seen_at
-- forward without touching the user's own decision. A DONE finding that comes
-- back reopens — that single rule is the entire regression story. A DISMISSED
-- one stays dismissed, which is the entire suppression story.
INSERT INTO findings (id,account_id,business_id,key,source,category,title,body,steps,detail,result_ids,prompt_ids,sources,blocking,reach,priority)
VALUES (@id,@account_id,@business_id,@key,@source,@category,@title,@body,@steps,@detail,@result_ids,@prompt_ids,@sources,@blocking,@reach,@priority)
ON CONFLICT (business_id,key) DO UPDATE SET
  source=EXCLUDED.source,category=EXCLUDED.category,title=EXCLUDED.title,body=EXCLUDED.body,steps=EXCLUDED.steps,detail=EXCLUDED.detail,
  result_ids=EXCLUDED.result_ids,prompt_ids=EXCLUDED.prompt_ids,sources=EXCLUDED.sources,
  blocking=EXCLUDED.blocking,reach=EXCLUDED.reach,priority=EXCLUDED.priority,
  last_seen_at=now(),
  status=CASE WHEN findings.status='DONE' THEN 'OPEN' ELSE findings.status END,
  completed_at=CASE WHEN findings.status='DONE' THEN NULL ELSE findings.completed_at END,
  verified_at=CASE WHEN findings.status='DONE' THEN NULL ELSE findings.verified_at END;

-- name: VerifyCompletedFindings :exec
-- A completed finding the latest run did not reproduce is confirmed fixed. This
-- re-checks the finding, never the visibility that followed it, so it carries no
-- causal claim.
UPDATE findings SET verified_at=now()
WHERE business_id = @business_id AND account_id = @account_id
  AND status='DONE' AND verified_at IS NULL AND completed_at IS NOT NULL
  AND NOT (key = ANY(@seen_keys::text[]));

-- name: ListFindings :many
-- Active work is what the current audit still reproduces, so a finding the
-- evidence has moved past drops out of the queue without needing a retired
-- state. Both timestamps are the publishing transaction's now(), so "seen by the
-- current audit" is an exact comparison rather than a tolerance. The ordering is
-- the product's triage: blockers, then the work most within the business's own
-- control, then how many answers are affected.
--
-- category_order is passed in rather than written here as a literal, because the
-- sequence is catalog data owned by the visibility package; a copy in SQL would
-- drift the first time a category is added. An unknown category sorts last.
SELECT f.* FROM findings f
WHERE f.business_id = @business_id AND f.account_id = @account_id
  AND (f.status <> 'OPEN' OR f.last_seen_at >= COALESCE(
    (SELECT a.checked_at FROM site_audits a WHERE a.business_id = @business_id AND a.published), '-infinity'::timestamptz))
ORDER BY f.blocking DESC,
  COALESCE(array_position(@category_order::text[], f.category), array_length(@category_order::text[], 1) + 1),
  f.reach DESC, f.priority, f.key;

-- name: GetFinding :one
SELECT * FROM findings WHERE id = @id AND account_id = @account_id;

-- name: SetFindingStatus :one
UPDATE findings SET
  status = @status,
  dismissal_reason = CASE WHEN @status = 'DISMISSED' THEN sqlc.narg('dismissal_reason') ELSE NULL END,
  completed_at = CASE WHEN @status = 'DONE' THEN now() ELSE NULL END,
  dismissed_at = CASE WHEN @status = 'DISMISSED' THEN now() ELSE NULL END,
  verified_at = NULL
WHERE id = @id AND account_id = @account_id AND status = @previous_status
RETURNING *;

-- name: LoadMonitoringEvidence :many
-- Each citation carries the businesses the answer cited IT for, resolved through
-- mention_citations (05). Grouping the competitors under their own citation
-- rather than under the result is the whole point: every business named anywhere
-- in an answer is a much larger set than the businesses a given source was cited
-- for, and a finder that confuses the two recommends sources on invented
-- evidence.
SELECT r.id AS run_id,pr.id AS result_id,pr.prompt_id,p.text AS prompt,pr.response_text,
 EXISTS(SELECT 1 FROM mentions m WHERE m.prompt_result_id=pr.id AND m.subject='self') AS mentioned,
 COALESCE((SELECT json_agg(json_build_object(
    'url',c.url,'domain',c.domain,
	'passage',substring(COALESCE(pr.response_text,'') FROM c.text_start + 1 FOR GREATEST(c.text_end-c.text_start,0)),
    'competitors',COALESCE((SELECT array_agg(DISTINCT co.name ORDER BY co.name)
      FROM mention_citations mc JOIN mentions m ON m.id=mc.mention_id
      JOIN competitors co ON co.id=m.competitor_id WHERE mc.citation_id=c.id),'{}')
  ) ORDER BY c.cite_order) FROM citations c WHERE c.prompt_result_id=pr.id),'[]')::jsonb AS citations
FROM monitoring_runs r JOIN prompt_results pr ON pr.run_id=r.id JOIN prompts p ON p.id=pr.prompt_id JOIN businesses b ON b.id=r.business_id
WHERE r.business_id = @business_id AND b.account_id = @account_id AND r.analysis_completed_at IS NOT NULL
 AND r.id IN (SELECT id FROM monitoring_runs WHERE business_id = @business_id AND analysis_completed_at IS NOT NULL ORDER BY scheduled_for DESC LIMIT 4)
 AND pr.status='succeeded' ORDER BY r.scheduled_for DESC,pr.requested_at,pr.id;
