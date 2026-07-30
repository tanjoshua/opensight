-- +goose Up
-- The single definition of "an analyzed result" every metrics query gates on
-- (design 02, internal/metrics's former analyzedJoin/analyzedWhere/selfMentions
-- consts): a prompt_result whose run has completed analysis and which has a
-- result_analyses row. Putting the gate in a view rather than duplicating it
-- across every query file means a query can never gate differently by
-- accident, and sqlc type-checks every query against it.
CREATE VIEW analyzed_results AS
SELECT
  pr.*,
  r.scheduled_for,
  r.business_id,
  b.tenant_id,
  ra.sentiment,
  ra.keywords,
  EXISTS (
    SELECT 1 FROM mentions sm
    WHERE sm.prompt_result_id = pr.id AND sm.subject = 'self'
  ) AS has_self_mention
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
JOIN result_analyses ra ON ra.prompt_result_id = pr.id
WHERE r.analysis_completed_at IS NOT NULL;

-- +goose Down
DROP VIEW IF EXISTS analyzed_results;
