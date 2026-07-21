-- +goose Up
-- Analysis tables (design 02, "Analysis (derived, rebuildable)"). Every row here
-- is derived from prompt_results and can be wiped and rebuilt; the raw results
-- and runs tables are never touched by that rebuild.

CREATE TABLE result_analyses (
  -- prompt_result_id is both PK and FK: at most one analysis row per result, and
  -- AnalyzeResult upserts by it (05). No separate id, so no UUIDv7 CHECK — the
  -- value's validity comes from the prompt_results FK.
  prompt_result_id uuid PRIMARY KEY REFERENCES prompt_results(id) ON DELETE CASCADE,
  -- NULL sentiment == business not mentioned in this response; mention facts live
  -- exclusively in mentions, never derived from this table (design 02).
  sentiment text CHECK (sentiment IS NULL OR sentiment IN ('positive', 'neutral', 'negative', 'mixed')),
  keywords text[] NOT NULL DEFAULT '{}',
  excerpts jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(excerpts) = 'array'),
  analysis_model text NOT NULL CHECK (btrim(analysis_model) <> ''),
  extraction_version int NOT NULL,
  analyzed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE competitors (
  id uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (btrim(name) <> ''),
  website text CHECK (website IS NULL OR btrim(website) <> ''),
  aliases text[] NOT NULL DEFAULT '{}',           -- approved matching keys (exact pass, 05)
  suggested_aliases text[] NOT NULL DEFAULT '{}',  -- LLM-proposed, awaiting user approval (05/06)
  source text NOT NULL CHECK (source IN ('discovered', 'manual')),
  status text NOT NULL CHECK (status IN ('discovered', 'tracked', 'dismissed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT competitors_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  )
);

CREATE INDEX competitors_business_id_idx ON competitors (business_id);

CREATE TABLE mentions (
  id uuid PRIMARY KEY,
  prompt_result_id uuid NOT NULL REFERENCES prompt_results(id) ON DELETE CASCADE,
  subject text NOT NULL CHECK (subject IN ('self', 'competitor')),
  competitor_id uuid REFERENCES competitors(id) ON DELETE CASCADE,
  matched_by text NOT NULL CHECK (matched_by IN ('exact', 'llm')),
  mention_order int NOT NULL CHECK (mention_order >= 0),
  excerpt text NOT NULL CHECK (btrim(excerpt) <> ''),
  -- competitor_id is required iff subject = 'competitor' (design 02): a self
  -- mention never carries one, a competitor mention always does.
  CONSTRAINT mentions_competitor_id_subject_check CHECK (
    (subject = 'self' AND competitor_id IS NULL)
    OR (subject = 'competitor' AND competitor_id IS NOT NULL)
  ),
  CONSTRAINT mentions_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  )
);

CREATE INDEX mentions_prompt_result_id_idx ON mentions (prompt_result_id);
CREATE INDEX mentions_competitor_id_idx ON mentions (competitor_id);

CREATE TABLE citations (
  id uuid PRIMARY KEY,
  prompt_result_id uuid NOT NULL REFERENCES prompt_results(id) ON DELETE CASCADE,
  url text NOT NULL CHECK (btrim(url) <> ''),
  domain text NOT NULL CHECK (btrim(domain) <> ''),
  title text CHECK (title IS NULL OR btrim(title) <> ''),
  cite_order int NOT NULL CHECK (cite_order >= 0),
  -- best-effort inference from response context, not from fetching pages (design
  -- 02); 'unknown' is an honest value, not an error.
  subject text NOT NULL CHECK (subject IN ('business', 'competitor', 'other', 'unknown')),
  CONSTRAINT citations_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  )
);

CREATE INDEX citations_prompt_result_id_idx ON citations (prompt_result_id);

-- +goose Down
DROP TABLE IF EXISTS citations;
DROP TABLE IF EXISTS mentions;
DROP TABLE IF EXISTS competitors;
DROP TABLE IF EXISTS result_analyses;
