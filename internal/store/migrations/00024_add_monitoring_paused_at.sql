-- +goose Up
-- A tenant admin can pause scheduled monitoring for a business. NULL means
-- monitoring is running; a timestamp records when it was paused. The column
-- is only a scheduling gate: prompts, runs, and derived history are untouched,
-- so resuming picks the trend back up on the next weekly slot.
ALTER TABLE businesses ADD COLUMN monitoring_paused_at timestamptz;

-- +goose Down
ALTER TABLE businesses DROP COLUMN monitoring_paused_at;
