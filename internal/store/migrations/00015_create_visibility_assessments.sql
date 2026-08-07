-- +goose Up
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

-- +goose Down
DROP TABLE improvement_action_events;
DROP TABLE improvement_actions;
DROP TABLE visibility_assessments;
DROP TABLE evidence_artifacts;
DROP TABLE assessment_module_outcomes;
DROP TABLE assessment_generations;
