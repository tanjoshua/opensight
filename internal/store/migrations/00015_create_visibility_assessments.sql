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
  assessed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (generation_id, practice_key, subject_key)
);

CREATE TABLE opportunities (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  practice_key text NOT NULL,
  subject_key text NOT NULL,
  current_assessment_id uuid NOT NULL REFERENCES visibility_assessments(id),
  -- Non-null means the row was produced by the newest generation: still unmet
  -- and in the queue. The compiler rebuilds this half of the row wholesale.
  current_generation_id uuid REFERENCES assessment_generations(id) ON DELETE SET NULL,
  rank int NOT NULL,
  presentation jsonb NOT NULL,
  user_status text NOT NULL DEFAULT 'OPEN' CHECK (user_status IN ('OPEN','IN_PROGRESS','COMPLETED','DISMISSED')),
  dismissal_reason text CHECK (dismissal_reason IS NULL OR dismissal_reason IN ('NOT_RELEVANT','ALREADY_DONE','NOT_ACTIONABLE','TOO_MUCH_EFFORT','OTHER')),
  completion_baseline jsonb,
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (business_id, practice_key, subject_key)
);

-- The single definition of a focus item: backed by the newest generation,
-- still actionable, and among the top three by rank. Every read path joins
-- this view rather than recomputing the rule.
CREATE VIEW opportunity_focus AS
SELECT id, row_number() OVER (PARTITION BY business_id ORDER BY rank, practice_key, subject_key) <= 3 AS focus
FROM opportunities
WHERE current_generation_id IS NOT NULL AND user_status IN ('OPEN', 'IN_PROGRESS');

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
DROP VIEW opportunity_focus;
DROP TABLE opportunities;
DROP TABLE visibility_assessments;
DROP TABLE evidence_artifacts;
DROP TABLE assessment_generations;
