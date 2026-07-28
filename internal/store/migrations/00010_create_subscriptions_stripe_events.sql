-- +goose Up
CREATE TABLE subscriptions (
  tenant_id uuid PRIMARY KEY REFERENCES tenants(id),
  plan_code text NOT NULL CHECK (btrim(plan_code) <> ''),
  stripe_customer_id text UNIQUE CHECK (stripe_customer_id IS NULL OR btrim(stripe_customer_id) <> ''),
  stripe_subscription_id text UNIQUE CHECK (stripe_subscription_id IS NULL OR btrim(stripe_subscription_id) <> ''),
  stripe_status text CHECK (stripe_status IS NULL OR btrim(stripe_status) <> ''),
  past_due_since timestamptz,
  comped boolean NOT NULL DEFAULT false,
  current_period_end timestamptz,
  cancel_at_period_end boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE stripe_events (
  id text PRIMARY KEY CHECK (btrim(id) <> ''),
  type text NOT NULL CHECK (btrim(type) <> ''),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  received_at timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz
);

-- Every pre-existing tenant is operator-granted (design 08 "Operator comps").
INSERT INTO subscriptions (tenant_id, plan_code, comped)
SELECT id, 'starter', true FROM tenants;

DROP INDEX tenants_plan_id_idx;
ALTER TABLE tenants DROP COLUMN plan_id;
DROP TABLE plans;

-- +goose Down
CREATE TABLE plans (
  id uuid PRIMARY KEY,
  slug text NOT NULL UNIQUE,
  prompt_limit integer NOT NULL CHECK (prompt_limit > 0),
  run_interval text NOT NULL CHECK (btrim(run_interval) <> ''),
  platforms text[] NOT NULL CHECK (cardinality(platforms) > 0),
  CONSTRAINT plans_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  )
);

INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ('01950000-0000-7000-8000-000000000001', 'starter', 20, 'weekly', ARRAY['chatgpt']::text[]);

ALTER TABLE tenants ADD COLUMN plan_id uuid REFERENCES plans(id);
UPDATE tenants SET plan_id = '01950000-0000-7000-8000-000000000001';
ALTER TABLE tenants ALTER COLUMN plan_id SET NOT NULL;
CREATE INDEX tenants_plan_id_idx ON tenants (plan_id);

DROP TABLE stripe_events;
DROP TABLE subscriptions;
