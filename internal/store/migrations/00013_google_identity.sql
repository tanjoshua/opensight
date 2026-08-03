-- +goose Up
-- Google is now the only sign-in method (design 07 "Auth and accounts"):
-- Google has already verified the address, so there is no password to hash
-- and no verification/reset flow to build. google_sub is the stable OIDC
-- subject claim; nullable because operator-created rows (opensight user
-- create) exist before the first Google sign-in links them by email.
ALTER TABLE users DROP COLUMN password_hash;
ALTER TABLE users ADD COLUMN google_sub text UNIQUE;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS google_sub;
ALTER TABLE users ADD COLUMN password_hash text;
