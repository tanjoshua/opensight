-- +goose Up
-- Pre-launch cutover: raw run history and every derived artifact may be
-- discarded. Accounts, subscriptions, businesses, prompts, and manually
-- curated competitors remain.
DELETE FROM monitoring_runs;
DELETE FROM competitors WHERE source = 'discovered';

ALTER TABLE monitoring_runs DROP COLUMN workflow_id;
ALTER TABLE monitoring_runs ADD COLUMN job_id bigint NOT NULL;

ALTER TABLE businesses
  ADD COLUMN generation_id uuid,
  ADD COLUMN generation_job_id bigint,
  ADD COLUMN generation_status text,
  ADD COLUMN generation_stage text,
  ADD CONSTRAINT businesses_generation_state_check CHECK (
    (generation_id IS NULL AND generation_job_id IS NULL AND generation_status IS NULL AND generation_stage IS NULL)
    OR
    (generation_id IS NOT NULL AND generation_job_id IS NOT NULL AND (
      (generation_status = 'generating' AND generation_stage IN ('fetching_site', 'drafting'))
      OR (generation_status IN ('ready', 'failed') AND generation_stage IS NULL)
    ))
  );

-- +goose Down
ALTER TABLE businesses DROP CONSTRAINT businesses_generation_state_check;
ALTER TABLE businesses
  DROP COLUMN generation_stage,
  DROP COLUMN generation_status,
  DROP COLUMN generation_job_id,
  DROP COLUMN generation_id;

ALTER TABLE monitoring_runs DROP COLUMN job_id;
ALTER TABLE monitoring_runs ADD COLUMN workflow_id text NOT NULL DEFAULT 'migration-down';
ALTER TABLE monitoring_runs ALTER COLUMN workflow_id DROP DEFAULT;
