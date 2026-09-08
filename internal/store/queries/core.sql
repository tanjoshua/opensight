-- name: BusinessOwned :one
SELECT 1 FROM businesses WHERE id = @id AND account_id = @account_id;

-- name: InsertAccount :one
INSERT INTO accounts (id, name, slug) VALUES ($1, $2, $3) RETURNING created_at;

-- name: InsertUser :one
INSERT INTO users (id, email, google_sub) VALUES ($1, $2, $3) RETURNING created_at;

-- name: UpsertUserByEmail :one
INSERT INTO users (id, email) VALUES ($1, $2)
ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
RETURNING id, email, google_sub, created_at;

-- name: InsertAccountMembership :one
INSERT INTO account_memberships (account_id, user_id, role)
VALUES ($1, $2, $3) RETURNING created_at;

-- name: UpsertAccountMembership :one
INSERT INTO account_memberships (account_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (account_id, user_id) DO UPDATE SET role = account_memberships.role
RETURNING role, created_at;

-- name: InsertSubscription :exec
INSERT INTO subscriptions (account_id, plan_code, comped) VALUES ($1, $2, $3);

-- name: GetUserByGoogleSub :one
SELECT id, email, google_sub FROM users WHERE google_sub = $1;

-- name: GetUserByEmail :one
SELECT id, email, google_sub FROM users WHERE email = $1;

-- name: SetUserGoogleSub :exec
UPDATE users SET google_sub = $2 WHERE id = $1;

-- name: InsertSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3);

-- name: DeleteExpiredUserSessions :exec
DELETE FROM sessions WHERE user_id = $1 AND expires_at <= now();

-- name: GetSession :one
SELECT u.id, u.email, s.expires_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: ListAccountMembershipsForUser :many
SELECT a.id AS account_id, a.name, a.slug, am.role, am.created_at
FROM account_memberships am JOIN accounts a ON a.id = am.account_id
WHERE am.user_id = $1 ORDER BY a.name, a.id;

-- name: GetAccountContextBySlug :one
-- Membership is a LEFT JOIN so the row still comes back for a non-member,
-- with a NULL role. Only the platform owner is admitted on such a row
-- (store.ResolveAccountSession); everyone else is rejected as not found.
SELECT a.id AS account_id, a.name, a.slug, am.role,
       sub.plan_code, sub.comped, sub.stripe_subscription_id,
       sub.stripe_status, sub.past_due_since
FROM accounts a
LEFT JOIN account_memberships am ON am.account_id = a.id AND am.user_id = @user_id
LEFT JOIN subscriptions sub ON sub.account_id = a.id
WHERE a.slug = @slug;

-- name: ListAllAccounts :many
-- Every account on the platform, carrying @user_id's own role where they are
-- a member. Platform-owner only (see api.isPlatformOwner).
SELECT a.id AS account_id, a.name, a.slug, am.role, am.created_at
FROM accounts a
LEFT JOIN account_memberships am ON am.account_id = a.id AND am.user_id = @user_id
ORDER BY a.name, a.id;

-- name: GetAccountMembership :one
SELECT account_id, user_id, role, created_at FROM account_memberships
WHERE account_id = $1 AND user_id = $2;

-- name: GetAccountByID :one
SELECT id, name, slug, created_at FROM accounts WHERE id = $1;

-- name: ListAccountMembers :many
SELECT u.id AS user_id, u.email, u.google_sub, am.role, am.created_at
FROM account_memberships am JOIN users u ON u.id = am.user_id
WHERE am.account_id = $1 ORDER BY lower(u.email), u.id;

-- name: UpdateAccountMembershipRole :execrows
UPDATE account_memberships SET role = $3 WHERE account_id = $1 AND user_id = $2;

-- name: DeleteAccountMembership :execrows
DELETE FROM account_memberships WHERE account_id = $1 AND user_id = $2;

-- name: LockAccount :one
SELECT id FROM accounts WHERE id = $1 FOR UPDATE;

-- name: CountAccountOwners :one
SELECT count(*) FROM account_memberships WHERE account_id = $1 AND role = 'owner';

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: InsertBusiness :one
INSERT INTO businesses (
  id, account_id, status, name, website, aliases, category, services, location, activated_at
) VALUES (@id, @account_id, @status, @name, @website, @aliases, @category, @services, @location, @activated_at)
RETURNING created_at;

-- name: GetBusiness :one
SELECT id, account_id, status, name, website, aliases, category, services, location, created_at, activated_at,
       generation_id, generation_job_id, generation_status, generation_stage, monitoring_paused_at
FROM businesses WHERE id = @id AND account_id = @account_id;

-- name: ListBusinesses :many
SELECT id, account_id, status, name, website, aliases, category, services, location, created_at, activated_at,
       generation_id, generation_job_id, generation_status, generation_stage, monitoring_paused_at
FROM businesses WHERE account_id = $1 ORDER BY created_at;

-- name: ResolveAccountID :one
SELECT account_id FROM businesses WHERE id = $1;

-- name: ListMonitoringCandidates :many
SELECT b.id, b.account_id, b.activated_at, s.plan_code, s.comped,
       s.stripe_subscription_id, s.stripe_status, s.past_due_since
FROM businesses b
JOIN subscriptions s ON s.account_id = b.account_id
WHERE b.status = 'active' AND b.activated_at IS NOT NULL
  AND b.monitoring_paused_at IS NULL;

-- name: SetBusinessMonitoringPaused :one
-- Idempotent in both directions: re-pausing keeps the original paused_at so
-- the UI's "paused since" does not drift, and resuming clears it outright.
UPDATE businesses
SET monitoring_paused_at = CASE
      WHEN @paused::bool THEN COALESCE(monitoring_paused_at, now())
      ELSE NULL
    END
WHERE id = @business_id AND account_id = @account_id AND status = 'active'
RETURNING id, account_id, status, name, website, aliases, category, services, location, created_at, activated_at,
          generation_id, generation_job_id, generation_status, generation_stage, monitoring_paused_at;

-- name: RenameAccount :exec
UPDATE accounts SET name = $2 WHERE id = $1;

-- name: UpdateActiveBusinessProfile :one
UPDATE businesses
SET name = CASE WHEN @name_set::bool THEN @name ELSE name END,
    website = CASE WHEN @website_set::bool THEN @website ELSE website END,
    aliases = CASE WHEN @aliases_set::bool THEN @aliases::text[] ELSE aliases END,
    category = CASE WHEN @category_set::bool THEN @category ELSE category END,
    services = CASE WHEN @services_set::bool THEN @services::jsonb ELSE services END,
    location = CASE WHEN @location_set::bool THEN @location::jsonb ELSE location END
WHERE id = @business_id AND account_id = @account_id AND status = 'active'
RETURNING id, account_id, status, name, website, aliases, category, services, location, created_at, activated_at,
          generation_id, generation_job_id, generation_status, generation_stage, monitoring_paused_at;

-- name: UpdateDraftBusinessWebsite :one
UPDATE businesses
SET website = @website
WHERE id = @business_id AND account_id = @account_id AND status = 'draft'
RETURNING id, account_id, status, name, website, aliases, category, services, location, created_at, activated_at,
          generation_id, generation_job_id, generation_status, generation_stage, monitoring_paused_at;

-- name: InstallBusinessGeneration :one
UPDATE businesses
SET website = @website,
    generation_id = @generation_id,
    generation_job_id = @generation_job_id,
    generation_status = 'generating',
    generation_stage = 'fetching_site'
WHERE id = @business_id AND account_id = @account_id AND status = 'draft'
RETURNING generation_job_id;

-- name: UpdateBusinessGenerationStage :execrows
UPDATE businesses SET generation_stage = @stage
WHERE id = @business_id AND generation_id = @generation_id
  AND status = 'draft' AND generation_status = 'generating';

-- name: FinishBusinessGeneration :execrows
UPDATE businesses SET generation_status = @status, generation_stage = NULL
WHERE id = @business_id AND generation_id = @generation_id
  AND status = 'draft' AND generation_status = 'generating';

-- name: LockDraftBusiness :one
SELECT status FROM businesses WHERE id = @id AND account_id = @account_id FOR UPDATE;

-- name: ActivateBusiness :one
UPDATE businesses
SET name = $2, aliases = $3, category = $4, services = $5, location = $6,
    status = 'active', activated_at = $7,
    generation_id = NULL, generation_job_id = NULL,
    generation_status = NULL, generation_stage = NULL
WHERE id = $1
RETURNING id, account_id, status, name, website, aliases, category, services, location, created_at, activated_at,
          generation_id, generation_job_id, generation_status, generation_stage, monitoring_paused_at;

-- name: MarkProposalApplied :exec
UPDATE profile_proposals SET status = 'applied', resolved_at = now()
WHERE business_id = $1 AND status = 'pending';

-- name: LockBusinessPlanCode :one
SELECT s.plan_code
FROM businesses b JOIN subscriptions s ON s.account_id = b.account_id
WHERE b.id = @id AND b.account_id = @account_id FOR UPDATE OF b;

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
WHERE pr.id = @id AND b.account_id = @account_id;

-- name: LockPromptForReplace :one
SELECT pr.id, pr.business_id, pr.text, pr.status, pr.replaces_prompt_id, pr.created_at
FROM prompts pr JOIN businesses b ON b.id = pr.business_id
WHERE pr.id = @id AND b.account_id = @account_id FOR UPDATE OF pr;

-- name: RetirePrompt :exec
UPDATE prompts SET status = 'retired', retired_at = now() WHERE id = $1;

-- name: GetSubscriptionByAccount :one
SELECT account_id, plan_code, stripe_customer_id, stripe_subscription_id,
       stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end,
       created_at, updated_at
FROM subscriptions WHERE account_id = $1;

-- name: GetSubscriptionByCustomer :one
SELECT account_id, plan_code, stripe_customer_id, stripe_subscription_id,
       stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end,
       created_at, updated_at
FROM subscriptions WHERE stripe_customer_id = $1;

-- name: UpsertSubscription :exec
INSERT INTO subscriptions (
  account_id, plan_code, stripe_customer_id, stripe_subscription_id,
  stripe_status, past_due_since, comped, current_period_end, cancel_at_period_end
) VALUES (@account_id,@plan_code,@stripe_customer_id,@stripe_subscription_id,@stripe_status,@past_due_since,@comped,@current_period_end,@cancel_at_period_end)
ON CONFLICT (account_id) DO UPDATE SET
  plan_code=EXCLUDED.plan_code, stripe_customer_id=EXCLUDED.stripe_customer_id,
  stripe_subscription_id=EXCLUDED.stripe_subscription_id, stripe_status=EXCLUDED.stripe_status,
  past_due_since=EXCLUDED.past_due_since, comped=EXCLUDED.comped,
  current_period_end=EXCLUDED.current_period_end, cancel_at_period_end=EXCLUDED.cancel_at_period_end,
  updated_at=now();

-- name: SetStripeCustomerID :one
UPDATE subscriptions
SET stripe_customer_id=COALESCE(stripe_customer_id,$2),
    updated_at=CASE WHEN stripe_customer_id IS NULL THEN now() ELSE updated_at END
WHERE account_id=$1 RETURNING stripe_customer_id;

-- name: InsertPendingProposal :one
INSERT INTO profile_proposals (id,business_id,payload,status)
VALUES ($1,$2,$3,'pending') RETURNING created_at;

-- name: GenerationIsCurrent :one
SELECT EXISTS(
  SELECT 1 FROM businesses
  WHERE id = @business_id AND generation_id = @generation_id
    AND status = 'draft' AND generation_status = 'generating'
);

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
