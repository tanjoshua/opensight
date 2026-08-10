-- +goose Up
-- The two quotations that argue a content finding: what a monitored answer
-- actually wrote about a competitor, and what the customer's own site says on
-- the same subject. Both were already computed and then discarded — the answer
-- passage only to count sources, the site passage not at all — so the card could
-- assert a gap without ever showing it. One column rather than three: coverage,
-- the cited passages and the site passages are read and written together, and a
-- finding either has the whole comparison or none of it.
ALTER TABLE findings ADD COLUMN comparison jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE findings DROP COLUMN comparison;
