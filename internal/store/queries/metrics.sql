-- name: KeywordStats :many
SELECT kw::text AS keyword,coalesce(array_agg(DISTINCT id),'{}')::uuid[] AS result_ids
FROM analyzed_results CROSS JOIN LATERAL unnest(keywords) AS kw
WHERE business_id = @business_id AND tenant_id = @tenant_id
GROUP BY kw ORDER BY count(DISTINCT id) DESC,kw;

-- name: SentimentStats :many
SELECT sentiment,coalesce(array_agg(id ORDER BY id),'{}')::uuid[] AS result_ids
FROM analyzed_results WHERE business_id = @business_id AND tenant_id = @tenant_id AND sentiment IS NOT NULL
GROUP BY sentiment ORDER BY count(*) DESC,sentiment;

-- name: CitationDomainStats :many
SELECT c.domain,coalesce(array_agg(DISTINCT ar.id),'{}')::uuid[] AS result_ids
FROM analyzed_results ar JOIN citations c ON c.prompt_result_id=ar.id
WHERE ar.business_id = @business_id AND ar.tenant_id = @tenant_id
GROUP BY c.domain ORDER BY count(DISTINCT ar.id) DESC,c.domain;

-- name: CitationSources :many
SELECT c.domain,c.url,c.title,c.subject,ar.prompt_id,p.text AS prompt_text,ar.id AS result_id
FROM analyzed_results ar JOIN citations c ON c.prompt_result_id=ar.id
JOIN prompts p ON p.id=ar.prompt_id AND p.business_id=ar.business_id
WHERE ar.business_id = @business_id AND ar.tenant_id = @tenant_id
ORDER BY c.domain,c.url,c.cite_order,p.text;

-- name: CompetitorOverall :many
SELECT co.id,co.name,co.status,co.aliases,co.suggested_aliases,
 count(DISTINCT am.result_id)::int AS mentioned,count(am.mention_id)::int AS total_mentions,
 coalesce(avg(am.mention_order),0)::float8 AS avg_order,
 coalesce(array_agg(DISTINCT am.result_id) FILTER (WHERE am.result_id IS NOT NULL),'{}')::uuid[] AS result_ids
FROM competitors co JOIN businesses owner ON owner.id=co.business_id
LEFT JOIN (
 SELECT m.competitor_id,m.id AS mention_id,m.mention_order,ar.id AS result_id
 FROM analyzed_results ar JOIN mentions m ON m.prompt_result_id=ar.id AND m.subject='competitor'
 WHERE ar.business_id = @business_id AND ar.tenant_id = @tenant_id
) am ON am.competitor_id=co.id
WHERE co.business_id = @business_id AND owner.tenant_id = @tenant_id
GROUP BY co.id,co.name,co.status,co.aliases,co.suggested_aliases
ORDER BY count(DISTINCT am.result_id) DESC,co.name;

-- name: CompetitorTrend :many
SELECT m.competitor_id,ar.run_id,ar.scheduled_for,count(DISTINCT ar.id)::int AS mentioned,
 coalesce(array_agg(DISTINCT ar.id),'{}')::uuid[] AS result_ids
FROM analyzed_results ar JOIN mentions m ON m.prompt_result_id=ar.id AND m.subject='competitor'
WHERE ar.business_id = @business_id AND ar.tenant_id = @tenant_id
GROUP BY m.competitor_id,ar.run_id,ar.scheduled_for ORDER BY ar.scheduled_for;

-- name: CompetitorPerPrompt :many
SELECT m.competitor_id,ar.prompt_id,p.text AS prompt_text,
 coalesce(array_agg(DISTINCT ar.id),'{}')::uuid[] AS result_ids
FROM analyzed_results ar JOIN mentions m ON m.prompt_result_id=ar.id AND m.subject='competitor'
JOIN prompts p ON p.id=ar.prompt_id
WHERE ar.business_id = @business_id AND ar.tenant_id = @tenant_id
GROUP BY m.competitor_id,ar.prompt_id,p.text ORDER BY count(DISTINCT ar.id) DESC,p.text;

-- name: PromptLatestStats :many
SELECT DISTINCT ON (ar.prompt_id) ar.prompt_id,ar.id AS result_id,ar.sentiment,
 coalesce(sm.mention_order,0)::int4 AS mention_order,(sm.prompt_result_id IS NOT NULL)::bool AS mentioned
FROM analyzed_results ar JOIN prompts p ON p.id=ar.prompt_id
LEFT JOIN (
 SELECT DISTINCT ON (prompt_result_id) prompt_result_id,mention_order FROM mentions
 WHERE subject='self' ORDER BY prompt_result_id,mention_order
) sm ON sm.prompt_result_id=ar.id
WHERE ar.business_id = @business_id AND ar.tenant_id = @tenant_id AND p.status='active'
ORDER BY ar.prompt_id,ar.requested_at DESC,ar.id DESC;

-- name: PromptTrends :many
SELECT ar.prompt_id,ar.run_id,ar.scheduled_for,ar.id AS result_id,ar.has_self_mention AS mentioned
FROM analyzed_results ar WHERE ar.business_id = @business_id AND ar.tenant_id = @tenant_id
ORDER BY ar.prompt_id,ar.scheduled_for,ar.id;

-- name: PromptChanges :many
SELECT d,sum(added)::int AS added,sum(retired)::int AS retired,sum(replaced)::int AS replaced
FROM (
 SELECT date_trunc('day',p.created_at)::timestamptz AS d,
 CASE WHEN p.replaces_prompt_id IS NULL THEN 1 ELSE 0 END AS added,0 AS retired,
 CASE WHEN p.replaces_prompt_id IS NOT NULL THEN 1 ELSE 0 END AS replaced
 FROM prompts p JOIN businesses b ON b.id=p.business_id
 WHERE b.id = @business_id AND b.tenant_id = @tenant_id
 UNION ALL
 SELECT date_trunc('day',p.retired_at)::timestamptz AS d,0,1,0
 FROM prompts p JOIN businesses b ON b.id=p.business_id
 WHERE b.id = @business_id AND b.tenant_id = @tenant_id AND p.retired_at IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM prompts r WHERE r.replaces_prompt_id=p.id
  AND date_trunc('day',r.created_at)=date_trunc('day',p.retired_at))
) e GROUP BY d ORDER BY d;

-- name: VisibilityTrend :many
SELECT run_id,scheduled_for,count(*)::int AS analyzed,
 count(*) FILTER (WHERE has_self_mention)::int AS mentioned,
 coalesce(array_agg(id ORDER BY id),'{}')::uuid[] AS result_ids
FROM analyzed_results WHERE business_id = @business_id AND tenant_id = @tenant_id
GROUP BY run_id,scheduled_for ORDER BY scheduled_for;
