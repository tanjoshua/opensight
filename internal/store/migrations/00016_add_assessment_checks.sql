-- +goose Up
-- checks records the individual assertions an assessor evaluated for one
-- practice subject, so the checklist can show what was tested rather than only
-- the rolled-up standing. Assessments published before this column keep an
-- empty list and render as not assessed until the next generation.
ALTER TABLE visibility_assessments
  ADD COLUMN checks jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE visibility_assessments DROP COLUMN checks;
