-- +goose Up
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

CREATE TABLE tenants (
  id uuid PRIMARY KEY,
  name text NOT NULL CHECK (btrim(name) <> ''),
  plan_id uuid NOT NULL REFERENCES plans(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT tenants_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  )
);

CREATE INDEX tenants_plan_id_idx ON tenants (plan_id);

CREATE TABLE users (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  email citext NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT users_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  )
);

CREATE INDEX users_tenant_id_idx ON users (tenant_id);

INSERT INTO plans (id, slug, prompt_limit, run_interval, platforms)
VALUES ('01950000-0000-7000-8000-000000000001', 'starter', 20, 'weekly', ARRAY['chatgpt']::text[]);

-- +goose Down
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
DROP TABLE IF EXISTS plans;
