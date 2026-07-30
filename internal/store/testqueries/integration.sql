-- name: TestQuery001 :exec
DELETE FROM businesses WHERE tenant_id = $1;

-- name: TestQuery002 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery003 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery004 :exec
INSERT INTO tenants (id, name) VALUES ($1, 'Out Of Order Tenant');

-- name: TestQuery005 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery006 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery007 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery008 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery009 :exec
INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Atlas Clinic');

-- name: TestQuery010 :exec
INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Other Clinic');

-- name: TestQuery011 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'root canal clinic', 'active');

-- name: TestQuery012 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best specialist near me', 'active');

-- name: TestQuery013 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'excluded prompt', 'active');

-- name: TestQuery014 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'foreign prompt text', 'active');

-- name: TestQuery015 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-06', 'completed', 'wf-cite-a', now(), now());

-- name: TestQuery016 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'wf-cite-b', now());

-- name: TestQuery017 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text, requested_at, completed_at)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{}'::jsonb, '{"id":"r"}'::jsonb, 'text', now(), now());

-- name: TestQuery018 :exec
INSERT INTO result_analyses (prompt_result_id, keywords, excerpts, analysis_model, extraction_version)
VALUES ($1, ARRAY['useful']::text[], '[]'::jsonb, 'gpt-5-mini', 1);

-- name: TestQuery019 :exec
INSERT INTO citations (id, prompt_result_id, url, domain, title, subject, cite_order)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: TestQuery020 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery021 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery022 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery023 :exec
INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Atlas Clinic');

-- name: TestQuery024 :exec
INSERT INTO prompts (id, business_id, text, status, created_at) VALUES ($1, $2, 'q', 'active', '2026-07-06T00:00:00Z');

-- name: TestQuery025 :exec
INSERT INTO competitors (id, business_id, name, aliases, suggested_aliases, source, status)
VALUES ($1, $2, 'Rival Clinic', ARRAY['rival clinic']::text[], ARRAY['Rival Medical']::text[], 'discovered', 'discovered');

-- name: TestQuery026 :exec
INSERT INTO competitors (id, business_id, name, source, status)
VALUES ($1, $2, 'Manual Rival', 'manual', 'tracked');

-- name: TestQuery027 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-06', 'completed', 'wf-a', now(), now());

-- name: TestQuery028 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'wf-b', now());

-- name: TestQuery029 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text, requested_at, completed_at)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{}'::jsonb, '{"id":"r"}'::jsonb, 'text', $4, $4);

-- name: TestQuery030 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, error, request, requested_at, completed_at)
VALUES ($1, $2, $3, 'failed', 'boom', '{}'::jsonb, now(), now());

-- name: TestQuery031 :exec
INSERT INTO result_analyses (prompt_result_id, sentiment, keywords, excerpts, analysis_model, extraction_version)
VALUES ($1, $2, ARRAY[$3::text]::text[], '[]'::jsonb, 'gpt-5-mini', 1);

-- name: TestQuery032 :exec
INSERT INTO citations (id, prompt_result_id, url, domain, subject, cite_order)
VALUES ($1, $2, 'https://x', $3, 'other', 0);

-- name: TestQuery033 :exec
INSERT INTO mentions (id, prompt_result_id, subject, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'self', 'exact', $3, 'ex');

-- name: TestQuery034 :exec
INSERT INTO mentions (id, prompt_result_id, subject, competitor_id, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'competitor', $3, 'exact', $4, 'ex');

-- name: TestQuery035 :exec
UPDATE prompts SET status = 'retired', retired_at = '2026-07-20T10:00:00Z' WHERE id = $1;

-- name: TestQuery036 :exec
INSERT INTO prompts (id, business_id, text, status, replaces_prompt_id, created_at)
VALUES ($1, $2, 'q2', 'active', $3, '2026-07-20T10:00:00Z');

-- name: TestQuery037 :exec
INSERT INTO prompts (id, business_id, text, status, created_at)
VALUES ($1, $2, 'q3', 'active', '2026-07-20T10:00:00Z');

-- name: TestQuery038 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery039 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery040 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery041 :exec
INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Atlas Clinic');

-- name: TestQuery042 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic near me', 'active');

-- name: TestQuery043 :exec
INSERT INTO competitors (id, business_id, name, aliases, source, status)
VALUES ($1, $2, 'Rival Clinic', ARRAY['rival clinic']::text[], 'manual', 'tracked');

-- name: TestQuery044 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-06', 'completed', 'wf-zero-a', now(), now());

-- name: TestQuery045 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'wf-zero-b', now(), now());

-- name: TestQuery046 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text, requested_at, completed_at)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{}'::jsonb, '{"id":"r"}'::jsonb, 'text', $4, $4);

-- name: TestQuery047 :exec
INSERT INTO result_analyses (prompt_result_id, keywords, excerpts, analysis_model, extraction_version)
VALUES ($1, ARRAY['useful']::text[], '[]'::jsonb, 'gpt-5-mini', 1);

-- name: TestQuery048 :exec
INSERT INTO mentions (id, prompt_result_id, subject, competitor_id, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'competitor', $3, 'exact', 0, 'Rival Clinic appears.');

-- name: TestQuery049 :exec
INSERT INTO tenants (id, name) VALUES ($1, $2);

-- name: TestQuery050 :exec
INSERT INTO subscriptions (tenant_id, plan_code, comped) VALUES ($1, 'starter', true);

-- name: TestQuery051 :exec
DELETE FROM users WHERE tenant_id = $1;

-- name: TestQuery052 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery053 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery054 :one
SELECT plan_code, comped FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery055 :one
SELECT password_hash FROM users WHERE id = $1;

-- name: TestQuery056 :exec
DELETE FROM users WHERE tenant_id = $1;

-- name: TestQuery057 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery058 :exec
DELETE FROM businesses WHERE tenant_id = $1;

-- name: TestQuery059 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery060 :one
SELECT plan_code, comped, stripe_customer_id, stripe_subscription_id, stripe_status FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery061 :exec
DELETE FROM users WHERE tenant_id = $1;

-- name: TestQuery062 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery063 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery064 :one
SELECT count(*) FROM tenants;

-- name: TestQuery065 :one
SELECT count(*) FROM tenants;

-- name: TestQuery066 :exec
DELETE FROM mentions WHERE prompt_result_id = $1;

-- name: TestQuery067 :exec
DELETE FROM citations WHERE prompt_result_id = $1;

-- name: TestQuery068 :exec
DELETE FROM result_analyses WHERE prompt_result_id = $1;

-- name: TestQuery069 :exec
DELETE FROM competitors WHERE id = $1;

-- name: TestQuery070 :exec
DELETE FROM prompt_results WHERE id = $1;

-- name: TestQuery071 :exec
DELETE FROM monitoring_runs WHERE id = $1;

-- name: TestQuery072 :exec
DELETE FROM prompts WHERE id = $1;

-- name: TestQuery073 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery074 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery075 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery076 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Analysis Clinic', 'clinic', '{"country":"SG"}'::jsonb, now());

-- name: TestQuery077 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic near me', 'active');

-- name: TestQuery078 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'analysis-workflow', now());

-- name: TestQuery079 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_1"}'::jsonb, 'Analysis Clinic and Rival Clinic are options.');

-- name: TestQuery080 :exec
INSERT INTO competitors (id, business_id, name, aliases, source, status)
VALUES ($1, $2, 'Rival Clinic', ARRAY['rival clinic']::text[], 'discovered', 'discovered');

-- name: TestQuery081 :exec
INSERT INTO result_analyses (prompt_result_id, sentiment, keywords, excerpts, analysis_model, extraction_version)
VALUES ($1, 'positive', ARRAY['friendly']::text[], '["Analysis Clinic is a good option."]'::jsonb, 'gpt-5-mini', 1);

-- name: TestQuery082 :exec
INSERT INTO citations (id, prompt_result_id, url, domain, subject, cite_order)
VALUES ($1, $2, 'https://example.com/x', 'example.com', 'business', 0);

-- name: TestQuery083 :exec
INSERT INTO mentions (id, prompt_result_id, subject, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'self', 'exact', 0, 'Analysis Clinic ... options.');

-- name: TestQuery084 :exec
INSERT INTO mentions (id, prompt_result_id, subject, competitor_id, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'competitor', $3, 'exact', 1, 'Rival Clinic ... options.');

-- name: TestQuery085 :exec
INSERT INTO mentions (id, prompt_result_id, subject, competitor_id, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'self', $3, 'exact', 2, 'bad');

-- name: TestQuery086 :exec
INSERT INTO mentions (id, prompt_result_id, subject, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'competitor', 'exact', 2, 'bad');

-- name: TestQuery087 :one
SELECT count(*) FROM result_analyses WHERE prompt_result_id = $1;

-- name: TestQuery088 :one
SELECT count(*) FROM citations WHERE prompt_result_id = $1;

-- name: TestQuery089 :one
SELECT count(*) FROM mentions WHERE prompt_result_id = $1;

-- name: TestQuery090 :one
SELECT count(*) FROM mentions WHERE prompt_result_id = $1;

-- name: TestQuery091 :one
SELECT count(*) FROM prompt_results WHERE id = $1;

-- name: TestQuery092 :one
SELECT count(*) FROM monitoring_runs WHERE id = $1;

-- name: TestQuery093 :exec
DELETE FROM competitors WHERE business_id IN ($1, $2);

-- name: TestQuery094 :exec
DELETE FROM businesses WHERE id IN ($1, $2);

-- name: TestQuery095 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery096 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery097 :exec
INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Atlas Clinic');

-- name: TestQuery098 :exec
INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Other Clinic');

-- name: TestQuery099 :exec
INSERT INTO competitors (id, business_id, name, aliases, source, status)
VALUES ($1, $2, 'Discovered Co', ARRAY['disco']::text[], 'discovered', 'discovered');

-- name: TestQuery100 :exec
INSERT INTO competitors (id, business_id, name, source, status)
VALUES ($1, $2, 'Tracked Co', 'manual', 'tracked');

-- name: TestQuery101 :exec
INSERT INTO competitors (id, business_id, name, source, status)
VALUES ($1, $2, 'Dismissed Co', 'discovered', 'dismissed');

-- name: TestQuery102 :exec
INSERT INTO competitors (id, business_id, name, source, status)
VALUES ($1, $2, 'Other Co', 'discovered', 'discovered');

-- name: TestQuery103 :exec
DELETE FROM prompts WHERE business_id IN ($1, $2);

-- name: TestQuery104 :exec
DELETE FROM profile_proposals WHERE business_id IN ($1, $2);

-- name: TestQuery105 :exec
DELETE FROM businesses WHERE id IN ($1, $2);

-- name: TestQuery106 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery107 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery108 :exec
INSERT INTO businesses (id, tenant_id, status, name, website)
VALUES ($1, $2, 'draft', 'Draft Clinic', 'https://draft.example');

-- name: TestQuery109 :exec
INSERT INTO profile_proposals (id, business_id, payload, status)
VALUES ($1, $2, '{"low_confidence":false}'::jsonb, 'pending');

-- name: TestQuery110 :exec
INSERT INTO businesses (id, tenant_id, status, name)
VALUES ($1, $2, 'draft', 'Manual Clinic');

-- name: TestQuery111 :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: TestQuery112 :exec
DELETE FROM users WHERE id = $1;

-- name: TestQuery113 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery114 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery115 :exec
INSERT INTO users (id, tenant_id, email, password_hash) VALUES ($1, $2, $3, $4);

-- name: TestQuery116 :exec
INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() - interval '1 minute');

-- name: TestQuery117 :one
SELECT count(*) FROM sessions WHERE user_id = $1 AND expires_at <= now();

-- name: TestQuery118 :exec
DELETE FROM users WHERE id = $1;

-- name: TestQuery119 :one
SELECT count(*) FROM sessions WHERE user_id = $1;

-- name: TestQuery120 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery121 :exec
DELETE FROM subscriptions WHERE tenant_id IN ($1, $2);

-- name: TestQuery122 :exec
DELETE FROM tenants WHERE id IN ($1, $2);

-- name: TestQuery123 :exec
INSERT INTO businesses
(id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Owner Clinic', 'clinic', '{"country":"SG"}', now());

-- name: TestQuery124 :exec
INSERT INTO prompts (id, business_id, text, status)
VALUES ($1, $2, 'best clinic', 'active');

-- name: TestQuery125 :exec
INSERT INTO monitoring_runs
(id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-20', 'completed', 'competitor-history', now(), now());

-- name: TestQuery126 :exec
INSERT INTO prompt_results
(id, run_id, prompt_id, status, model, request, raw_response, response_text, requested_at, completed_at)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{}', '{}', 'text', now(), now());

-- name: TestQuery127 :exec
INSERT INTO result_analyses
(prompt_result_id, analysis_model, extraction_version)
VALUES ($1, 'gpt-5-mini', 1);

-- name: TestQuery128 :exec
INSERT INTO mentions
(id, prompt_result_id, subject, competitor_id, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'competitor', $3, 'exact', 0, 'Rival Clinic');

-- name: TestQuery129 :exec
UPDATE competitors
SET suggested_aliases = ARRAY['Private Alias']::text[]
WHERE id = $1;

-- name: TestQuery130 :one
SELECT count(*) FROM mentions WHERE competitor_id = $1;

-- name: TestQuery131 :exec
DELETE FROM profile_proposals WHERE business_id = $1;

-- name: TestQuery132 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery133 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery134 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery135 :exec
INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Proposal Clinic');

-- name: TestQuery136 :exec
DELETE FROM prompts WHERE business_id = $1;

-- name: TestQuery137 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery138 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery139 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery140 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Prompt Limit Clinic', 'clinic', '{"country":"SG"}'::jsonb, now());

-- name: TestQuery141 :one
SELECT count(*) FROM prompts WHERE business_id = $1 AND status = 'active';

-- name: TestQuery142 :exec
DELETE FROM prompts WHERE business_id = $1;

-- name: TestQuery143 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery144 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery145 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery146 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Replace Clinic', 'clinic', '{"country":"SG"}'::jsonb, now());

-- name: TestQuery147 :one
SELECT status FROM prompts WHERE id = $1;

-- name: TestQuery148 :one
SELECT count(*) FROM prompts WHERE business_id = $1 AND status = 'active';

-- name: TestQuery149 :exec
DELETE FROM prompt_results WHERE run_id IN (SELECT id FROM monitoring_runs WHERE business_id = $1);

-- name: TestQuery150 :exec
DELETE FROM competitors WHERE business_id = $1;

-- name: TestQuery151 :exec
DELETE FROM monitoring_runs WHERE business_id = $1;

-- name: TestQuery152 :exec
DELETE FROM prompts WHERE business_id = $1;

-- name: TestQuery153 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery154 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery155 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery156 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Result Analysis Clinic', 'clinic', '{"country":"SG"}'::jsonb, now());

-- name: TestQuery157 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic near me', 'active');

-- name: TestQuery158 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'cheapest clinic near me', 'active');

-- name: TestQuery159 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at, analysis_completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'result-analysis-workflow', now(), now());

-- name: TestQuery160 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_1"}'::jsonb, 'Clinic and Rival are options.');

-- name: TestQuery161 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_2"}'::jsonb, 'No mention here.');

-- name: TestQuery162 :exec
INSERT INTO competitors (id, business_id, name, source, status)
VALUES ($1, $2, 'Rival Clinic', 'discovered', 'discovered');

-- name: TestQuery163 :exec
INSERT INTO result_analyses (prompt_result_id, sentiment, keywords, excerpts, analysis_model, extraction_version)
VALUES ($1, 'positive', ARRAY['friendly','affordable']::text[], '["Clinic is a good option."]'::jsonb, 'gpt-5-mini', 1);

-- name: TestQuery164 :exec
INSERT INTO mentions (id, prompt_result_id, subject, matched_by, mention_order, verbatim_name, excerpt)
VALUES ($1, $2, 'self', 'exact', 0, 'Atlas Clinic', 'Clinic ... options.');

-- name: TestQuery165 :exec
INSERT INTO mentions (id, prompt_result_id, subject, competitor_id, matched_by, mention_order, verbatim_name, excerpt)
VALUES ($1, $2, 'competitor', $3, 'llm', 1, 'Rival Clinic', 'Rival ... options.');

-- name: TestQuery166 :exec
INSERT INTO citations (id, prompt_result_id, url, domain, title, cite_order, subject)
VALUES ($1, $2, 'https://example.com/x', 'example.com', 'Example', 0, 'business');

-- name: TestQuery167 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_1"}'::jsonb, 'Clinic is an option.');

-- name: TestQuery168 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5-mini', '{"model":"gpt-5-mini"}'::jsonb, '{"id":"resp_2"}'::jsonb, 'Nobody relevant.');

-- name: TestQuery169 :exec
INSERT INTO result_analyses (prompt_result_id, sentiment, keywords, excerpts, analysis_model, extraction_version)
VALUES ($1, 'positive', '{}'::text[], '[]'::jsonb, 'gpt-5-mini', 1);

-- name: TestQuery170 :exec
INSERT INTO mentions (id, prompt_result_id, subject, matched_by, mention_order, excerpt)
VALUES ($1, $2, 'self', 'exact', 0, 'Clinic ... option.');

-- name: TestQuery171 :exec
DELETE FROM prompt_results WHERE id = $1;

-- name: TestQuery172 :exec
DELETE FROM monitoring_runs WHERE id = $1;

-- name: TestQuery173 :exec
DELETE FROM prompts WHERE id = $1;

-- name: TestQuery174 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery175 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery176 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery177 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Runs Results Clinic', 'clinic', '{"country":"SG"}'::jsonb, now());

-- name: TestQuery178 :exec
INSERT INTO prompts (id, business_id, text, status)
VALUES ($1, $2, 'best clinic near me', 'active');

-- name: TestQuery179 :exec
INSERT INTO monitoring_runs (
  id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at
) VALUES (
  $1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'runs-results-workflow', now()
);

-- name: TestQuery180 :exec
INSERT INTO prompt_results (
  id, run_id, prompt_id, status, model, request, raw_response, response_text
) VALUES (
  $1,
  $2,
  $3,
  'succeeded',
  'gpt-5-mini-2026-07-01',
  '{"model":"gpt-5-mini","user_location":{"country":"SG"}}'::jsonb,
  '{"id":"resp_1","model":"gpt-5-mini-2026-07-01"}'::jsonb,
  'Runs Results Clinic is a good option.'
);

-- name: TestQuery181 :exec
INSERT INTO monitoring_runs (
  id, business_id, platform, trigger, scheduled_for, status, workflow_id
) VALUES (
  $1, $2, 'chatgpt', 'manual', '2026-07-13', 'running', 'runs-results-workflow-duplicate'
);

-- name: TestQuery182 :exec
INSERT INTO prompt_results (
  id, run_id, prompt_id, status, model, request, raw_response, response_text
) VALUES (
  $1,
  $2,
  $3,
  'succeeded',
  'gpt-5-mini-2026-07-01',
  '{"model":"gpt-5-mini"}'::jsonb,
  '{"id":"resp_2"}'::jsonb,
  'Duplicate response.'
);

-- name: TestQuery183 :exec
UPDATE prompt_results SET response_text = 'Changed response.' WHERE id = $1;

-- name: TestQuery184 :exec
DELETE FROM prompt_results WHERE id = $1;

-- name: TestQuery185 :exec
DELETE FROM prompts WHERE id = $1;

-- name: TestQuery186 :exec
INSERT INTO prompts (id, business_id, text, status)
VALUES ($1, $2, 'cheapest clinic near me', 'active');

-- name: TestQuery187 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, request, error)
VALUES ($1, $2, $3, 'failed', '{"model":"gpt-5-mini"}'::jsonb, 'openai: timeout');

-- name: TestQuery188 :exec
DELETE FROM prompt_results WHERE run_id = $1;

-- name: TestQuery189 :exec
DELETE FROM monitoring_runs WHERE id = $1;

-- name: TestQuery190 :exec
DELETE FROM prompts WHERE id = $1;

-- name: TestQuery191 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery192 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery193 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery194 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Finalize Partial Clinic', 'clinic', '{"country":"SG"}'::jsonb, now());

-- name: TestQuery195 :exec
INSERT INTO prompts (id, business_id, text, status)
VALUES ($1, $2, 'best clinic near me', 'active');

-- name: TestQuery196 :exec
DELETE FROM prompts WHERE id = $1;

-- name: TestQuery197 :exec
INSERT INTO prompts (id, business_id, text, status)
VALUES ($1, $2, 'cheapest clinic near me', 'active');

-- name: TestQuery198 :exec
DELETE FROM prompt_results WHERE run_id IN (SELECT id FROM monitoring_runs WHERE business_id = $1);

-- name: TestQuery199 :exec
DELETE FROM monitoring_runs WHERE business_id = $1;

-- name: TestQuery200 :exec
DELETE FROM prompts WHERE business_id = $1;

-- name: TestQuery201 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery202 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery203 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery204 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'List Runs Counts Clinic', 'clinic', '{"country":"SG"}'::jsonb, now());

-- name: TestQuery205 :exec
INSERT INTO prompts (id, business_id, text, status)
VALUES ($1, $4, 'a', 'active'), ($2, $4, 'b', 'active'), ($3, $4, 'c', 'active');

-- name: TestQuery206 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, expected_results, completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-20', 'partial', 'run-with-results', 3, now()),
       ($3, $2, 'chatgpt', 'scheduled', '2026-07-13', 'running', 'run-with-no-results', 2, NULL);

-- name: TestQuery207 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $3, $4, 'succeeded', 'gpt-5-mini-2026-07-01', '{}'::jsonb, '{}'::jsonb, 'ok'),
       ($2, $3, $5, 'succeeded', 'gpt-5-mini-2026-07-01', '{}'::jsonb, '{}'::jsonb, 'ok');

-- name: TestQuery208 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, request, error)
VALUES ($1, $2, $3, 'failed', '{}'::jsonb, 'openai: timeout');

-- name: TestQuery209 :exec
INSERT INTO result_analyses (prompt_result_id, analysis_model, extraction_version)
VALUES ($1, 'gpt-5-mini-2026-07-01', 1);

-- name: TestQuery210 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery211 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery212 :exec
INSERT INTO tenants (id, name) VALUES ($1, 'Subscription Tenant');

-- name: TestQuery213 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery214 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery215 :exec
INSERT INTO tenants (id, name) VALUES ($1, 'Customer Id Tenant');

-- name: TestQuery216 :exec
DELETE FROM prompt_results WHERE run_id IN (SELECT id FROM monitoring_runs WHERE business_id = $1);

-- name: TestQuery217 :exec
DELETE FROM monitoring_runs WHERE business_id = $1;

-- name: TestQuery218 :exec
DELETE FROM profile_proposals WHERE business_id = $1;

-- name: TestQuery219 :exec
DELETE FROM prompts WHERE business_id = $1;

-- name: TestQuery220 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery221 :exec
DELETE FROM subscriptions WHERE tenant_id = ANY($1);

-- name: TestQuery222 :exec
DELETE FROM tenants WHERE id = ANY($1);

-- name: TestQuery223 :one
SELECT name FROM tenants WHERE id = $1;

-- name: TestQuery224 :one
SELECT count(*) FROM prompts WHERE business_id = $1;

-- name: TestQuery225 :exec
INSERT INTO tenants (id, name) VALUES ($1, $2);

-- name: TestQuery226 :exec
INSERT INTO subscriptions (tenant_id, plan_code, comped) VALUES ($1, 'starter', true);

-- name: TestQuery227 :exec
DELETE FROM prompt_results WHERE prompt_id = $1;

-- name: TestQuery228 :exec
DELETE FROM monitoring_runs WHERE business_id = $1;

-- name: TestQuery229 :exec
DELETE FROM prompts WHERE id = $1;

-- name: TestQuery230 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery231 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery232 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery233 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Activities Clinic', 'clinic', '{"country":"SG","city":"Singapore"}'::jsonb, now());

-- name: TestQuery234 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic near me', 'active');

-- name: TestQuery235 :one
SELECT count(*) FROM monitoring_runs WHERE business_id = $1 AND scheduled_for = $2;

-- name: TestQuery236 :exec
DELETE FROM citations WHERE prompt_result_id = $1;

-- name: TestQuery237 :exec
DELETE FROM result_analyses WHERE prompt_result_id = $1;

-- name: TestQuery238 :exec
DELETE FROM prompt_results WHERE id = $1;

-- name: TestQuery239 :exec
DELETE FROM monitoring_runs WHERE id = $1;

-- name: TestQuery240 :exec
DELETE FROM prompts WHERE id = $1;

-- name: TestQuery241 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery242 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery243 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery244 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Atlas Dental', 'clinic', '{"country":"SG","city":"Singapore"}'::jsonb, now());

-- name: TestQuery245 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic for braces', 'active');

-- name: TestQuery246 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'analyze-wf', now());

-- name: TestQuery247 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5', '{"model":"gpt-5"}'::jsonb, $4::jsonb, $5);

-- name: TestQuery248 :one
SELECT sentiment, analysis_model, extraction_version FROM result_analyses WHERE prompt_result_id = $1;

-- name: TestQuery249 :one
SELECT url, domain, subject, cite_order FROM citations WHERE prompt_result_id = $1;

-- name: TestQuery250 :exec
DELETE FROM citations WHERE prompt_result_id = $1;

-- name: TestQuery251 :exec
DELETE FROM result_analyses WHERE prompt_result_id = $1;

-- name: TestQuery252 :one
SELECT count(*) FROM result_analyses WHERE prompt_result_id = $1;

-- name: TestQuery253 :one
SELECT count(*) FROM result_analyses WHERE prompt_result_id = $1;

-- name: TestQuery254 :one
SELECT count(*) FROM citations WHERE prompt_result_id = $1;

-- name: TestQuery255 :exec
DELETE FROM profile_proposals WHERE business_id = $1;

-- name: TestQuery256 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery257 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery258 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery259 :exec
INSERT INTO businesses (id, tenant_id, status, name) VALUES ($1, $2, 'draft', 'Persist Clinic');

-- name: TestQuery260 :exec
DELETE FROM mentions WHERE prompt_result_id = $1;

-- name: TestQuery261 :exec
DELETE FROM competitors WHERE business_id = $1;

-- name: TestQuery262 :exec
DELETE FROM prompt_results WHERE id = $1;

-- name: TestQuery263 :exec
DELETE FROM monitoring_runs WHERE id = $1;

-- name: TestQuery264 :exec
DELETE FROM prompts WHERE id = $1;

-- name: TestQuery265 :exec
DELETE FROM businesses WHERE id = $1;

-- name: TestQuery266 :exec
DELETE FROM subscriptions WHERE tenant_id = $1;

-- name: TestQuery267 :exec
DELETE FROM tenants WHERE id = $1;

-- name: TestQuery268 :exec
INSERT INTO businesses (id, tenant_id, status, name, category, location, activated_at)
VALUES ($1, $2, 'active', 'Atlas Dental', 'clinic', '{"country":"SG"}'::jsonb, now());

-- name: TestQuery269 :exec
INSERT INTO prompts (id, business_id, text, status) VALUES ($1, $2, 'best clinic', 'active');

-- name: TestQuery270 :exec
INSERT INTO monitoring_runs (id, business_id, platform, trigger, scheduled_for, status, workflow_id, completed_at)
VALUES ($1, $2, 'chatgpt', 'scheduled', '2026-07-13', 'completed', 'reconcile-wf', now());

-- name: TestQuery271 :exec
INSERT INTO prompt_results (id, run_id, prompt_id, status, model, request, raw_response, response_text)
VALUES ($1, $2, $3, 'succeeded', 'gpt-5', '{"model":"gpt-5"}'::jsonb, '{"id":"r"}'::jsonb, 'text');

-- name: TestQuery272 :exec
INSERT INTO competitors (id, business_id, name, aliases, source, status)
VALUES ($1, $2, 'Bravo Clinic', ARRAY['bravo clinic']::text[], 'manual', 'tracked');

-- name: TestQuery273 :one
SELECT status, source FROM competitors WHERE business_id = $1 AND name = 'Charlie Medical';

-- name: TestQuery274 :one
SELECT to_jsonb(aliases) FROM competitors WHERE business_id = $1 AND name = 'Charlie Medical';

-- name: TestQuery275 :one
SELECT to_jsonb(suggested_aliases) FROM competitors WHERE id = $1;

-- name: TestQuery276 :one
SELECT to_jsonb(aliases) FROM competitors WHERE id = $1;

-- name: TestQuery277 :one
SELECT analysis_completed_at FROM monitoring_runs WHERE id = $1;

-- name: TestQuery278 :one
SELECT count(*) FROM competitors WHERE business_id = $1 AND name = 'Charlie Medical';

-- name: TestQuery279 :one
SELECT count(*) FROM mentions WHERE prompt_result_id = $1;

-- name: TestQuery280 :one
SELECT to_jsonb(suggested_aliases) FROM competitors WHERE id = $1;

-- name: TestQuery281 :one
SELECT matched_by, verbatim_name FROM mentions WHERE prompt_result_id = $1 AND subject = $2 AND mention_order = $3;

-- name: TestQuery282 :exec
INSERT INTO tenants (id, name) VALUES ($1, $2);

-- name: TestQuery283 :exec
INSERT INTO subscriptions (tenant_id, plan_code, comped) VALUES ($1, 'starter', true);

-- name: TestQuery284 :one
SELECT count(*) FROM prompts WHERE business_id = $1 AND status = 'active';

-- name: TestQuery285 :one
SELECT status FROM profile_proposals WHERE id = $1;

-- name: TestQuery286 :exec
DELETE FROM mentions WHERE prompt_result_id IN (
  SELECT pr.id FROM prompt_results pr
  JOIN monitoring_runs r ON r.id = pr.run_id
  WHERE r.business_id = $1
);

-- name: TestQuery287 :exec
DELETE FROM citations WHERE prompt_result_id IN (
  SELECT pr.id FROM prompt_results pr
  JOIN monitoring_runs r ON r.id = pr.run_id
  WHERE r.business_id = $1
);

-- name: TestQuery288 :exec
DELETE FROM result_analyses WHERE prompt_result_id IN (
  SELECT pr.id FROM prompt_results pr
  JOIN monitoring_runs r ON r.id = pr.run_id
  WHERE r.business_id = $1
);
