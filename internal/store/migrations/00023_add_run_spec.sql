-- +goose Up
ALTER TABLE monitoring_runs
ADD COLUMN spec jsonb NOT NULL DEFAULT '{}'::jsonb
CHECK (jsonb_typeof(spec) = 'object');

-- Direct SQL fixtures and historical rows may use the empty object. The
-- application run writer always supplies the complete immutable run spec.

-- +goose Down
ALTER TABLE monitoring_runs DROP COLUMN spec;
