-- +goose Up
-- The Improve feature is rebuilt around two pipelines instead of one catalog of
-- "practices": a deterministic site audit with a closed set of checks, and an
-- open set of findings discovered from evidence. Six tables collapse to two.
-- Everything dropped here is derived data — the next monitoring run regenerates
-- the audit and the findings from the responses and the site, which are the only
-- primary records involved.
DROP TABLE IF EXISTS improvement_action_events;
DROP TABLE IF EXISTS improvement_actions;
DROP TABLE IF EXISTS visibility_assessments;
DROP TABLE IF EXISTS evidence_artifacts;
DROP TABLE IF EXISTS assessment_module_outcomes;
DROP TABLE IF EXISTS assessment_generations;

-- site_audits holds one run of the check catalog. checks carries every result,
-- because a check is only ever read as part of the whole audit and a row per
-- check would buy nothing but joins. A non-null failure means the crawl itself
-- did not happen, in which case every check reports as unverifiable.
CREATE TABLE site_audits (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  monitoring_run_id uuid NOT NULL REFERENCES monitoring_runs(id) ON DELETE CASCADE,
  checked_at timestamptz NOT NULL DEFAULT now(),
  pages_read int NOT NULL DEFAULT 0,
  failure text,
  checks jsonb NOT NULL DEFAULT '[]'::jsonb,
  published boolean NOT NULL DEFAULT false,
  UNIQUE (monitoring_run_id)
);

-- Exactly one audit is current per business; the rest are history.
CREATE UNIQUE INDEX site_audits_current ON site_audits (business_id) WHERE published;

-- findings is the work queue. key is the readable identity a finder reproduces
-- each run ("site-audit:pages_allow_indexing", "citation-gap:healthhub.sg"), so
-- the same problem next week updates this row rather than creating a second one.
--
-- The lifecycle is deliberately three states. A finding is active when it is
-- OPEN and was seen by the current audit, so one the evidence stops producing
-- drops off without needing a retired state. DONE reopens if the finding comes
-- back, which is the only regression rule there is. DISMISSED never reopens.
-- verified_at records the first run after completion that did not reproduce the
-- finding: a re-check confirming the fix landed, never a claim about visibility.
CREATE TABLE findings (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  key text NOT NULL,
  source text NOT NULL,
  -- category is the kind of change the finding asks for, and the axis the work
  -- queue filters on: the three checklist groups for work on the customer's own
  -- site, plus one for getting listed elsewhere. Deliberately unconstrained, so
  -- a later finder introducing a category is a code change and not a migration.
  category text NOT NULL,
  title text NOT NULL,
  body text NOT NULL,
  steps jsonb NOT NULL DEFAULT '[]'::jsonb,
  detail text NOT NULL DEFAULT '',
  result_ids uuid[] NOT NULL DEFAULT '{}',
  prompt_ids uuid[] NOT NULL DEFAULT '{}',
  sources text[] NOT NULL DEFAULT '{}',
  blocking boolean NOT NULL DEFAULT false,
  reach int NOT NULL DEFAULT 0,
  priority int NOT NULL DEFAULT 2,
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','DONE','DISMISSED')),
  dismissal_reason text CHECK (dismissal_reason IS NULL OR dismissal_reason IN
    ('NOT_RELEVANT','ALREADY_DONE','NOT_ACTIONABLE','TOO_MUCH_EFFORT','OTHER')),
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  dismissed_at timestamptz,
  verified_at timestamptz,
  UNIQUE (business_id, key)
);

CREATE INDEX findings_business_status ON findings (business_id, status);

-- +goose Down
DROP TABLE IF EXISTS findings;
DROP TABLE IF EXISTS site_audits;

CREATE TABLE assessment_generations (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  monitoring_run_id uuid NOT NULL REFERENCES monitoring_runs(id) ON DELETE CASCADE,
  status text NOT NULL CHECK (status IN ('RUNNING','READY','PARTIAL','FAILED')),
  compiler_version int NOT NULL,
  ranker_version int NOT NULL,
  module_plan jsonb NOT NULL DEFAULT '[]'::jsonb,
  error text,
  started_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  UNIQUE (monitoring_run_id)
);

CREATE TABLE assessment_module_outcomes (
  generation_id uuid NOT NULL REFERENCES assessment_generations(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  assessor_key text NOT NULL,
  status text NOT NULL CHECK (status IN ('SUCCEEDED','FAILED','SKIPPED')),
  error text,
  completed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (generation_id, assessor_key)
);

CREATE TABLE evidence_artifacts (
  id uuid PRIMARY KEY,
  generation_id uuid NOT NULL REFERENCES assessment_generations(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  collector_key text NOT NULL,
  collector_version int NOT NULL,
  payload_version int NOT NULL,
  status text NOT NULL CHECK (status IN ('SUCCEEDED','FAILED')),
  checked_at timestamptz NOT NULL,
  payload jsonb NOT NULL,
  error text,
  UNIQUE (generation_id, collector_key)
);

CREATE TABLE visibility_assessments (
  id uuid PRIMARY KEY,
  generation_id uuid NOT NULL REFERENCES assessment_generations(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  practice_key text NOT NULL,
  criteria_version int NOT NULL,
  assessor_key text NOT NULL,
  assessor_version int NOT NULL,
  subject_key text NOT NULL,
  status text NOT NULL CHECK (status IN ('MET','PARTIAL','NOT_MET','UNKNOWN','NOT_APPLICABLE')),
  result_ids uuid[] NOT NULL DEFAULT '{}',
  prompt_ids uuid[] NOT NULL DEFAULT '{}',
  checked_sources text[] NOT NULL DEFAULT '{}',
  explanation text NOT NULL,
  reach int NOT NULL,
  persistence int NOT NULL,
  evidence_quality int NOT NULL,
  actionability int NOT NULL,
  effort int NOT NULL,
  payload_version int NOT NULL,
  payload jsonb NOT NULL,
  checks jsonb NOT NULL DEFAULT '[]'::jsonb,
  published boolean NOT NULL DEFAULT false,
  assessed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (generation_id, practice_key, subject_key)
);
CREATE UNIQUE INDEX visibility_assessments_current_subject
  ON visibility_assessments (business_id, practice_key, subject_key) WHERE published;

CREATE TABLE improvement_actions (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  practice_key text NOT NULL,
  subject_key text NOT NULL,
  cycle int NOT NULL CHECK (cycle > 0),
  recommendation_key text NOT NULL,
  current_assessment_id uuid REFERENCES visibility_assessments(id),
  rank int NOT NULL,
  presentation jsonb NOT NULL,
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','IN_PROGRESS','COMPLETED','DISMISSED','RETIRED','SUPERSEDED')),
  dismissal_reason text CHECK (dismissal_reason IS NULL OR dismissal_reason IN ('NOT_RELEVANT','ALREADY_DONE','NOT_ACTIONABLE','TOO_MUCH_EFFORT','OTHER')),
  completion_baseline jsonb,
  started_at timestamptz,
  completed_at timestamptz,
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, practice_key, subject_key, cycle)
);
CREATE UNIQUE INDEX improvement_actions_active_cycle
  ON improvement_actions (business_id, practice_key, subject_key)
  WHERE status IN ('OPEN','IN_PROGRESS');

CREATE TABLE improvement_action_events (
  id uuid PRIMARY KEY,
  action_id uuid NOT NULL REFERENCES improvement_actions(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  event_key text NOT NULL,
  event_type text NOT NULL CHECK (event_type IN ('CREATED','STARTED','COMPLETED','DISMISSED','RESTORED','RETIRED','SUPERSEDED','RECURRED')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (action_id, event_key)
);
