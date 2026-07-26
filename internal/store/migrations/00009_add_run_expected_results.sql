-- +goose Up
ALTER TABLE monitoring_runs ADD COLUMN expected_results int
  CHECK (expected_results IS NULL OR expected_results >= 0);

-- +goose Down
ALTER TABLE monitoring_runs DROP COLUMN expected_results;
