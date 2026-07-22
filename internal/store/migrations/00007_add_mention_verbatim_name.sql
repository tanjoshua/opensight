-- +goose Up
-- Keep the exact extracted organization text on mention rows so evidence views
-- can highlight the mention itself while retaining excerpt as surrounding proof.
ALTER TABLE mentions ADD COLUMN verbatim_name text;
UPDATE mentions SET verbatim_name = excerpt;
ALTER TABLE mentions
  ADD CONSTRAINT mentions_verbatim_name_nonempty CHECK (
    verbatim_name IS NULL OR btrim(verbatim_name) <> ''
  );

-- +goose Down
ALTER TABLE mentions DROP CONSTRAINT IF EXISTS mentions_verbatim_name_nonempty;
ALTER TABLE mentions DROP COLUMN IF EXISTS verbatim_name;
