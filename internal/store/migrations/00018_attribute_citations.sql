-- +goose Up
-- A url_citation annotation marks where the model wrote its inline link, not
-- what the link supports; the text a citation backs is what precedes that
-- marker. text_start/text_end record that resolved range as character offsets
-- into prompt_results.response_text, so the evidence behind a citation can be
-- read back out of the answer instead of being inferred from the whole answer.
ALTER TABLE citations
  ADD COLUMN text_start integer NOT NULL DEFAULT 0,
  ADD COLUMN text_end   integer NOT NULL DEFAULT 0;
ALTER TABLE citations
  ALTER COLUMN text_start DROP DEFAULT,
  ALTER COLUMN text_end   DROP DEFAULT;

-- cite_order is already unique per result; stating it lets a mention resolve its
-- citation by (prompt_result_id, cite_order) in a provably single-row subselect.
CREATE UNIQUE INDEX citations_result_cite_order_idx ON citations (prompt_result_id, cite_order);

-- citation_id is the source the answer cited for this business. Null means the
-- answer named the business without citing anything for it — a real answer, not
-- a missing one. ON DELETE SET NULL because re-analysis deletes and reinserts a
-- result's citations, and that must never delete a mention.
ALTER TABLE mentions
  ADD COLUMN citation_id uuid REFERENCES citations (id) ON DELETE SET NULL;
CREATE INDEX mentions_citation_id_idx ON mentions (citation_id);

-- Rows written before this migration carry no attribution: their offsets are
-- zero and their citation_id is null until their run is re-analyzed.

-- +goose Down
ALTER TABLE mentions DROP COLUMN citation_id;
DROP INDEX citations_result_cite_order_idx;
ALTER TABLE citations DROP COLUMN text_start, DROP COLUMN text_end;
