-- Per-account weekly token/search usage over stored monitoring runs (RUN-6).
--
-- The cost model (design 04 "Cost model") is: one Responses call per prompt with
-- web search enabled — tokens plus per-search-call fees, ballpark $0.50-2 per
-- account per week. Token usage and web-search request counts live inside
-- prompt_results.raw_response (the OpenAI Responses payload), so cost reporting is
-- a query with no schema change. Run this against real runs to reality-check the
-- ballpark; multiply the token and num_requests columns by current OpenAI pricing
-- to get dollars (pricing is not stored — it changes independently of our data).
--
-- Week bucket: monitoring_runs.scheduled_for is the run's target date and the
-- natural weekly cadence (design 04 "Scheduling"). date_trunc collapses the
-- occasional two-runs-in-one-week case (initial run mid-week + first scheduled
-- run) into a single ISO-week row. Only status='succeeded' results carry a
-- non-null raw_response (prompt_results_payload_status_check), so failed prompts
-- contribute nothing and are excluded.

SELECT
  b.account_id,
  a.name AS account_name,
  date_trunc('week', r.scheduled_for)::date AS week,
  count(*) AS succeeded_prompts,
  sum((pr.raw_response -> 'usage' ->> 'input_tokens')::bigint)  AS input_tokens,
  sum((pr.raw_response -> 'usage' ->> 'output_tokens')::bigint) AS output_tokens,
  sum((pr.raw_response -> 'usage' ->> 'total_tokens')::bigint)  AS total_tokens,
  sum(COALESCE((pr.raw_response -> 'tool_usage' -> 'web_search' ->> 'num_requests')::bigint, 0)) AS web_search_requests
FROM prompt_results pr
JOIN monitoring_runs r ON r.id = pr.run_id
JOIN businesses b ON b.id = r.business_id
JOIN accounts a ON a.id = b.account_id
WHERE pr.status = 'succeeded'
GROUP BY b.account_id, a.name, week
ORDER BY week DESC, account_name;
