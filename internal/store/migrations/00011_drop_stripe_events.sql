-- +goose Up
DROP TABLE stripe_events;

-- +goose Down
CREATE TABLE stripe_events (
  id text PRIMARY KEY CHECK (btrim(id) <> ''),
  type text NOT NULL CHECK (btrim(type) <> ''),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  received_at timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz
);
