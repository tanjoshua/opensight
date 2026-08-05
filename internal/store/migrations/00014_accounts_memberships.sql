-- +goose Up
-- Accounts are the commercial/security boundary. Users are global identities
-- and receive per-account roles through account_memberships.
ALTER TABLE tenants RENAME TO accounts;
ALTER TABLE accounts RENAME CONSTRAINT tenants_pkey TO accounts_pkey;
ALTER TABLE accounts RENAME CONSTRAINT tenants_id_uuidv7 TO accounts_id_uuidv7;

-- The old tenant name was an internal provisioning label in some early
-- environments. The monitored business is the useful name people recognize.
UPDATE accounts a
SET name = (
  SELECT b.name
  FROM businesses b
  WHERE b.tenant_id = a.id
  ORDER BY b.created_at, b.id
  LIMIT 1
)
WHERE EXISTS (SELECT 1 FROM businesses b WHERE b.tenant_id = a.id);

ALTER TABLE accounts ADD COLUMN slug text;
UPDATE accounts
SET slug = COALESCE(
  NULLIF(trim(BOTH '-' FROM regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g')), ''),
  'account'
) || '-' || right(replace(id::text, '-', ''), 8);
ALTER TABLE accounts ALTER COLUMN slug SET NOT NULL;
ALTER TABLE accounts ADD CONSTRAINT accounts_slug_not_blank CHECK (btrim(slug) <> '');
ALTER TABLE accounts ADD CONSTRAINT accounts_slug_format CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$');
ALTER TABLE accounts ADD CONSTRAINT accounts_slug_key UNIQUE (slug);

ALTER TABLE businesses RENAME COLUMN tenant_id TO account_id;
ALTER TABLE businesses RENAME CONSTRAINT businesses_tenant_id_fkey TO businesses_account_id_fkey;
ALTER INDEX businesses_tenant_id_idx RENAME TO businesses_account_id_idx;

ALTER TABLE subscriptions RENAME COLUMN tenant_id TO account_id;
ALTER TABLE subscriptions RENAME CONSTRAINT subscriptions_tenant_id_fkey TO subscriptions_account_id_fkey;

CREATE TABLE account_memberships (
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role text NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (account_id, user_id)
);
CREATE INDEX account_memberships_user_id_idx ON account_memberships (user_id, account_id);

INSERT INTO account_memberships (account_id, user_id, role)
SELECT tenant_id, id, 'owner' FROM users;

DROP INDEX users_tenant_id_idx;
ALTER TABLE users DROP COLUMN tenant_id;

DROP VIEW analyzed_results;
CREATE VIEW analyzed_results AS
SELECT
  pr.*,
  r.scheduled_for,
  r.business_id,
  b.account_id,
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
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM users u
    LEFT JOIN account_memberships am ON am.user_id = u.id
    GROUP BY u.id HAVING count(am.account_id) <> 1
  ) THEN
    RAISE EXCEPTION 'cannot roll back accounts migration: every user must have exactly one account membership';
  END IF;
END $$;

ALTER TABLE users ADD COLUMN tenant_id uuid;
UPDATE users u
SET tenant_id = am.account_id
FROM account_memberships am
WHERE am.user_id = u.id;
ALTER TABLE users ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES accounts(id);
CREATE INDEX users_tenant_id_idx ON users (tenant_id);

DROP TABLE account_memberships;
ALTER TABLE subscriptions RENAME COLUMN account_id TO tenant_id;
ALTER TABLE subscriptions RENAME CONSTRAINT subscriptions_account_id_fkey TO subscriptions_tenant_id_fkey;
ALTER TABLE businesses RENAME COLUMN account_id TO tenant_id;
ALTER TABLE businesses RENAME CONSTRAINT businesses_account_id_fkey TO businesses_tenant_id_fkey;
ALTER INDEX businesses_account_id_idx RENAME TO businesses_tenant_id_idx;

ALTER TABLE accounts DROP COLUMN slug;
ALTER TABLE accounts RENAME CONSTRAINT accounts_pkey TO tenants_pkey;
ALTER TABLE accounts RENAME CONSTRAINT accounts_id_uuidv7 TO tenants_id_uuidv7;
ALTER TABLE accounts RENAME TO tenants;

DROP VIEW analyzed_results;
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
