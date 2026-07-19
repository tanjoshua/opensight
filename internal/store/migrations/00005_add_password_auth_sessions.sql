-- +goose Up
-- password_hash holds a PHC-encoded argon2id string (design 07 "Auth and
-- accounts"). Nullable: users created before AUTH-2 sets a credential have no
-- hash, and login treats NULL as an authentication failure (uniform 401).
ALTER TABLE users ADD COLUMN password_hash text;

-- sessions are server-side bearer credentials (design 07): a random opaque
-- token lives only in the client cookie; the DB stores its SHA-256 so a DB dump
-- or log leak yields no usable cookie. Deliberate deviations from the table
-- conventions elsewhere in this schema:
--   * The PK is the 32-byte token hash, NOT a UUIDv7 — the row is keyed by a
--     bearer secret, not a sortable application id, so no uuidv7 CHECK applies.
--   * The AC's "random token" is stored HASHED here, never in the clear.
CREATE TABLE sessions (
  token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- +goose Down
DROP TABLE IF EXISTS sessions;
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
