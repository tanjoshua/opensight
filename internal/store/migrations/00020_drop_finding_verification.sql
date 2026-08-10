-- +goose Up
-- Verification told a user that a later run stopped reproducing a completed
-- finding. It was the only claim the product made about work having landed, and
-- it was the weakest kind: a re-check of the finding, never of the visibility
-- that followed. Measuring whether a change actually moved answers is a
-- deliberate future design, not this column, so the column goes rather than
-- sitting unread and implying the question is already answered.
ALTER TABLE findings DROP COLUMN verified_at;

-- +goose Down
ALTER TABLE findings ADD COLUMN verified_at timestamptz;
