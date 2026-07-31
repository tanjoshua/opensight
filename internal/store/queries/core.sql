-- name: BusinessOwned :one
SELECT 1 FROM businesses WHERE id = $1 AND tenant_id = $2;

-- name: InsertTenant :one
INSERT INTO tenants (id, name) VALUES ($1, $2) RETURNING created_at;

-- name: InsertUser :one
INSERT INTO users (id, tenant_id, email, password_hash)
VALUES ($1, $2, $3, $4) RETURNING created_at;

-- name: InsertSubscription :exec
INSERT INTO subscriptions (tenant_id, plan_code, comped) VALUES ($1, $2, $3);

-- name: GetUserCredentials :one
SELECT u.id, u.tenant_id, u.email, t.name, u.password_hash
FROM users u JOIN tenants t ON t.id = u.tenant_id WHERE u.email = $1;

-- name: InsertSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3);

-- name: DeleteExpiredUserSessions :exec
DELETE FROM sessions WHERE user_id = $1 AND expires_at <= now();

-- name: GetSession :one
-- LEFT JOIN deliberately, not INNER: a missing subscriptions row must surface
-- to the caller as an explicit error (BILL-6), not silently masquerade as an
-- expired/absent session by disappearing from the result set.
SELECT u.id, u.tenant_id, u.email, t.name, s.expires_at,
       sub.plan_code, sub.comped, sub.stripe_subscription_id, sub.stripe_status, sub.past_due_since
FROM sessions s
JOIN users u ON u.id = s.user_id
JOIN tenants t ON t.id = u.tenant_id
LEFT JOIN subscriptions sub ON sub.tenant_id = u.tenant_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: InsertBusiness :one
INSERT INTO businesses (
  id, tenant_id, status, name, website, aliases, category, services, location, activated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING created_at;

-- name: GetBusiness :one
SELECT id, tenant_id, status, name, website, aliases, category, services, location, created_at, activated_at
FROM businesses WHERE id = $1 AND tenant_id = $2;

-- name: ListBusinesses :many
SELECT id, tenant_id, status, name, website, aliases, category, services, location, created_at, activated_at
FROM businesses WHERE tenant_id = $1 ORDER BY created_at;

-- name: ResolveTenantID :one
SELECT tenant_id FROM businesses WHERE id = $1;

-- name: RenameTenant :exec
UPDATE tenants SET name = $2 WHERE id = $1;

-- name: UpdateActiveBusinessProfile :one
UPDATE businesses
SET name = CASE WHEN @name_set::bool THEN @name ELSE name END,
    website = CASE WHEN @website_set::bool THEN @website ELSE website END,
    aliases = CASE WHEN @aliases_set::bool THEN @aliases::text[] ELSE aliases END,
    category = CASE WHEN @category_set::bool THEN @category ELSE category END,
    services = CASE WHEN @services_set::bool THEN @services::jsonb ELSE services END,
    location = CASE WHEN @location_set::bool THEN @location::jsonb ELSE location END
WHERE id = @business_id AND tenant_id = @tenant_id AND status = 'active'
RETURNING id, tenant_id, status, name, website, aliases, category, services, location, created_at, activated_at;

-- name: LockDraftBusiness :one
SELECT status FROM businesses WHERE id = $1 AND tenant_id = $2 FOR UPDATE;

-- name: ActivateBusiness :one
UPDATE businesses
SET name = $2, aliases = $3, category = $4, services = $5, location = $6,
    status = 'active', activated_at = $7
WHERE id = $1
RETURNING id, tenant_id, status, name, website, aliases, category, services, location, created_at, activated_at;

-- name: MarkProposalApplied :exec
UPDATE profile_proposals SET status = 'applied', resolved_at = now()
WHERE business_id = $1 AND status = 'pending';

-- name: LockBusinessPlanCode :one
SELECT s.plan_code
FROM businesses b JOIN subscriptions s ON s.tenant_id = b.tenant_id
WHERE b.id = $1 AND b.tenant_id = $2 FOR UPDATE OF b;

-- name: CountActivePrompts :one
SELECT count(*) FROM prompts WHERE business_id = $1 AND status = 'active';

-- name: InsertActivePrompt :one
INSERT INTO prompts (id, business_id, text, status, replaces_prompt_id)
VALUES ($1, $2, $3, 'active', $4) RETURNING created_at;

-- name: ListActivePrompts :many
SELECT id, business_id, text, status, replaces_prompt_id, created_at
FROM prompts WHERE business_id = $1 AND status = 'active' ORDER BY created_at;

-- name: GetPrompt :one
SELECT pr.id, pr.business_id, pr.text, pr.status, pr.replaces_prompt_id, pr.created_at
FROM prompts pr JOIN businesses b ON b.id = pr.business_id
WHERE pr.id = $1 AND b.tenant_id = $2;

-- name: LockPromptForReplace :one
SELECT pr.id, pr.business_id, pr.text, pr.status, pr.replaces_prompt_id, pr.created_at
FROM prompts pr JOIN businesses b ON b.id = pr.business_id
WHERE pr.id = $1 AND b.tenant_id = $2 FOR UPDATE OF pr;

-- name: RetirePrompt :exec
UPDATE prompts SET status = 'retired', retired_at = now() WHERE id = $1;

-- name: GetSubscriptionByTenant :one
SELECT tenant_id, plan_code, stripe_customer_id, stripe_subscription_id,
       stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end,
       created_at, updated_at
FROM subscriptions WHERE tenant_id = $1;

-- name: GetSubscriptionByCustomer :one
SELECT tenant_id, plan_code, stripe_customer_id, stripe_subscription_id,
       stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end,
       created_at, updated_at
FROM subscriptions WHERE stripe_customer_id = $1;

-- name: UpsertSubscription :exec
INSERT INTO subscriptions (
  tenant_id, plan_code, stripe_customer_id, stripe_subscription_id,
  stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (tenant_id) DO UPDATE SET
  plan_code=EXCLUDED.plan_code, stripe_customer_id=EXCLUDED.stripe_customer_id,
  stripe_subscription_id=EXCLUDED.stripe_subscription_id, stripe_status=EXCLUDED.stripe_status,
  past_due_since=EXCLUDED.past_due_since, comped=EXCLUDED.comped,
  current_period_end=EXCLUDED.current_period_end, cancel_at_period_end=EXCLUDED.cancel_at_period_end,
  updated_at=now();

-- name: SetStripeCustomerID :one
UPDATE subscriptions
SET stripe_customer_id=COALESCE(stripe_customer_id,$2),
    updated_at=CASE WHEN stripe_customer_id IS NULL THEN now() ELSE updated_at END
WHERE tenant_id=$1 RETURNING stripe_customer_id;

-- name: InsertPendingProposal :one
INSERT INTO profile_proposals (id,business_id,payload,status)
VALUES ($1,$2,$3,'pending') RETURNING created_at;

-- name: GetPendingProposal :one
SELECT id,business_id,payload,status,created_at,resolved_at
FROM profile_proposals WHERE business_id=$1 AND status='pending';

-- name: DiscardPendingProposal :exec
UPDATE profile_proposals SET status='discarded',resolved_at=now()
WHERE business_id=$1 AND status='pending';

-- name: AcquireAdvisoryLock :exec
SELECT pg_advisory_lock(hashtextextended($1, 0));

-- name: ReleaseAdvisoryLock :one
SELECT pg_advisory_unlock(hashtextextended($1, 0));
