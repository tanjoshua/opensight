-- +goose Up
CREATE TABLE businesses (
  id uuid PRIMARY KEY,
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  status text NOT NULL CHECK (status IN ('draft', 'active')),
  name text NOT NULL CHECK (btrim(name) <> ''),
  website text,
  aliases text[] NOT NULL DEFAULT ARRAY[]::text[],
  category text,
  practitioners jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(practitioners) = 'array'),
  services jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(services) = 'array'),
  location jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  activated_at timestamptz,
  CONSTRAINT businesses_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  ),
  CONSTRAINT businesses_location_object_check CHECK (
    location IS NULL OR jsonb_typeof(location) = 'object'
  ),
  CONSTRAINT businesses_draft_not_activated_check CHECK (
    status = 'active' OR activated_at IS NULL
  ),
  CONSTRAINT businesses_active_profile_check CHECK (
    status = 'draft'
    OR (
      activated_at IS NOT NULL
      AND category IS NOT NULL
      AND btrim(category) <> ''
      AND location IS NOT NULL
      AND btrim(COALESCE(location->>'country', '')) <> ''
    )
  )
);

CREATE INDEX businesses_tenant_id_idx ON businesses (tenant_id);

CREATE TABLE profile_proposals (
  id uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  status text NOT NULL CHECK (status IN ('pending', 'applied', 'discarded')),
  created_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  CONSTRAINT profile_proposals_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  ),
  CONSTRAINT profile_proposals_resolution_check CHECK (
    (status = 'pending' AND resolved_at IS NULL)
    OR (status IN ('applied', 'discarded') AND resolved_at IS NOT NULL)
  )
);

CREATE INDEX profile_proposals_business_id_idx ON profile_proposals (business_id);
CREATE UNIQUE INDEX profile_proposals_one_pending_per_business_idx
  ON profile_proposals (business_id)
  WHERE status = 'pending';

CREATE TABLE prompts (
  id uuid PRIMARY KEY,
  business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
  text text NOT NULL CHECK (btrim(text) <> ''),
  status text NOT NULL CHECK (status IN ('active', 'retired')),
  replaces_prompt_id uuid REFERENCES prompts(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  retired_at timestamptz,
  CONSTRAINT prompts_id_uuidv7 CHECK (
    substring(id::text from 15 for 1) = '7'
    AND substring(id::text from 20 for 1) IN ('8', '9', 'a', 'b')
  ),
  CONSTRAINT prompts_retirement_check CHECK (
    (status = 'active' AND retired_at IS NULL)
    OR (status = 'retired' AND retired_at IS NOT NULL)
  ),
  CONSTRAINT prompts_no_self_replacement_check CHECK (
    replaces_prompt_id IS NULL OR replaces_prompt_id <> id
  )
);

CREATE INDEX prompts_business_id_idx ON prompts (business_id);
CREATE INDEX prompts_replaces_prompt_id_idx ON prompts (replaces_prompt_id)
  WHERE replaces_prompt_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION reject_prompt_text_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.text IS DISTINCT FROM OLD.text THEN
    RAISE EXCEPTION 'prompt text is immutable after insert';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER prompts_reject_text_update
  BEFORE UPDATE OF text ON prompts
  FOR EACH ROW
  EXECUTE FUNCTION reject_prompt_text_update();

-- +goose Down
DROP TRIGGER IF EXISTS prompts_reject_text_update ON prompts;
DROP FUNCTION IF EXISTS reject_prompt_text_update();
DROP TABLE IF EXISTS prompts;
DROP TABLE IF EXISTS profile_proposals;
DROP TABLE IF EXISTS businesses;
