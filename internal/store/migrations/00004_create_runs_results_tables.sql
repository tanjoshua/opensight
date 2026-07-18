-- +goose Up
CREATE TABLE monitoring_runs (
  id uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  platform text NOT NULL CHECK (btrim(platform) <> ''),
  trigger text NOT NULL CHECK (trigger IN ('initial', 'scheduled', 'manual')),
  scheduled_for date NOT NULL,
  status text NOT NULL CHECK (status IN ('running', 'completed', 'partial', 'failed')),
  workflow_id text NOT NULL CHECK (btrim(workflow_id) <> ''),
  started_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  analysis_completed_at timestamptz,
  CONSTRAINT monitoring_runs_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  ),
  CONSTRAINT monitoring_runs_completion_check CHECK (
    (status = 'running' AND completed_at IS NULL)
    OR (status IN ('completed', 'partial', 'failed') AND completed_at IS NOT NULL)
  ),
  CONSTRAINT monitoring_runs_analysis_after_completion_check CHECK (
    analysis_completed_at IS NULL
    OR completed_at IS NOT NULL
  ),
  UNIQUE (business_id, platform, scheduled_for)
);

CREATE INDEX monitoring_runs_business_id_idx ON monitoring_runs (business_id);

CREATE TABLE prompt_results (
  id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES monitoring_runs(id) ON DELETE CASCADE,
  prompt_id uuid NOT NULL REFERENCES prompts(id),
  status text NOT NULL CHECK (status IN ('succeeded', 'failed')),
  model text CHECK (model IS NULL OR btrim(model) <> ''),
  request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object'),
  raw_response jsonb CHECK (raw_response IS NULL OR jsonb_typeof(raw_response) = 'object'),
  response_text text CHECK (response_text IS NULL OR btrim(response_text) <> ''),
  error text CHECK (error IS NULL OR btrim(error) <> ''),
  requested_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT prompt_results_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  ),
  CONSTRAINT prompt_results_payload_status_check CHECK (
    (
      status = 'succeeded'
      AND model IS NOT NULL
      AND raw_response IS NOT NULL
      AND response_text IS NOT NULL
      AND error IS NULL
    )
    OR (
      status = 'failed'
      AND error IS NOT NULL
    )
  ),
  CONSTRAINT prompt_results_completed_after_requested_check CHECK (
    completed_at >= requested_at
  ),
  UNIQUE (run_id, prompt_id)
);

CREATE INDEX prompt_results_prompt_id_idx ON prompt_results (prompt_id);

-- +goose StatementBegin
CREATE FUNCTION reject_prompt_result_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'prompt results are append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER prompt_results_reject_update
  BEFORE UPDATE ON prompt_results
  FOR EACH ROW
  EXECUTE FUNCTION reject_prompt_result_update();

-- +goose Down
DROP TRIGGER IF EXISTS prompt_results_reject_update ON prompt_results;
DROP FUNCTION IF EXISTS reject_prompt_result_update();
DROP TABLE IF EXISTS prompt_results;
DROP TABLE IF EXISTS monitoring_runs;
