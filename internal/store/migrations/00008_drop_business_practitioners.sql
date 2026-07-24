-- +goose Up
ALTER TABLE businesses DROP COLUMN practitioners;

-- +goose Down
ALTER TABLE businesses ADD COLUMN practitioners jsonb NOT NULL DEFAULT '[]'::jsonb
  CHECK (jsonb_typeof(practitioners) = 'array');
