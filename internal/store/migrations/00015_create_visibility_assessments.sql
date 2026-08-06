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
  rollout_mode text NOT NULL CHECK (rollout_mode IN ('DISABLED','SHADOW','ACTIVE')),
  result_ids uuid[] NOT NULL DEFAULT '{}',
  prompt_ids uuid[] NOT NULL DEFAULT '{}',
  checked_sources text[] NOT NULL DEFAULT '{}',
  confidence double precision NOT NULL CHECK (confidence BETWEEN 0 AND 1),
  explanation text NOT NULL,
  reach int NOT NULL,
  persistence int NOT NULL,
  evidence_quality int NOT NULL,
  actionability int NOT NULL,
  effort int NOT NULL,
  payload_version int NOT NULL,
  payload jsonb NOT NULL,
  assessed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (generation_id, practice_key, subject_key)
);
CREATE INDEX visibility_assessments_current_idx ON visibility_assessments
  (business_id, practice_key, criteria_version, assessed_at DESC);

CREATE TABLE opportunities (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  practice_key text NOT NULL,
  subject_key text NOT NULL,
  current_assessment_id uuid NOT NULL REFERENCES visibility_assessments(id),
  rank int NOT NULL,
  presentation jsonb NOT NULL,
  user_status text NOT NULL DEFAULT 'OPEN' CHECK (user_status IN ('OPEN','IN_PROGRESS','COMPLETED','DISMISSED')),
  dismissal_reason text CHECK (dismissal_reason IS NULL OR dismissal_reason IN ('NOT_RELEVANT','ALREADY_DONE','NOT_ACTIONABLE','TOO_MUCH_EFFORT','OTHER')),
  completion_baseline jsonb,
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, practice_key, subject_key)
);
CREATE INDEX opportunities_current_idx ON opportunities (business_id, rank)
  WHERE user_status <> 'DISMISSED';

CREATE TABLE opportunity_events (
  id uuid PRIMARY KEY,
  opportunity_id uuid NOT NULL REFERENCES opportunities(id) ON DELETE CASCADE,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  event_key text NOT NULL,
  event_type text NOT NULL CHECK (event_type IN ('STARTED','DISMISSED','RESTORED','COMPLETED','OUTCOME_OBSERVED')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (opportunity_id, event_key)
);

-- +goose Down
DROP TABLE opportunity_events;
DROP TABLE opportunities;
DROP TABLE visibility_assessments;
DROP TABLE evidence_artifacts;
DROP TABLE assessment_generations;
