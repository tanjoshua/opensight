-- +goose Up
-- All analysis and Improve rows are derived. Existing rows used positional
-- citation attribution and cannot be converted faithfully, so retain the raw
-- runs/results and configuration while clearing every derived output.
DELETE FROM findings;
DELETE FROM site_audits;
DELETE FROM mentions;
DELETE FROM citations;
DELETE FROM result_analyses;
UPDATE monitoring_runs SET analysis_completed_at = NULL;

DROP INDEX mentions_citation_id_idx;
ALTER TABLE mentions DROP COLUMN citation_id;

CREATE TABLE mention_citations (
  mention_id uuid NOT NULL REFERENCES mentions(id) ON DELETE CASCADE,
  citation_id uuid NOT NULL REFERENCES citations(id) ON DELETE CASCADE,
  PRIMARY KEY (mention_id, citation_id)
);
CREATE INDEX mention_citations_citation_id_idx ON mention_citations (citation_id);

-- +goose Down
DROP TABLE mention_citations;
ALTER TABLE mentions
  ADD COLUMN citation_id uuid REFERENCES citations(id) ON DELETE SET NULL;
CREATE INDEX mentions_citation_id_idx ON mentions (citation_id);
