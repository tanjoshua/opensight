-- +goose Up
-- Everything else that references accounts already cascades (00014, 00015,
-- 00017). businesses and subscriptions predate accounts and still restrict,
-- which is what would block deleting a workspace; the whole account-scoped
-- tree hangs off these two, so cascading them makes DELETE FROM accounts the
-- one statement that removes a workspace.
ALTER TABLE businesses DROP CONSTRAINT businesses_account_id_fkey;
ALTER TABLE businesses ADD CONSTRAINT businesses_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_account_id_fkey;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_account_id_fkey;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id);

ALTER TABLE businesses DROP CONSTRAINT businesses_account_id_fkey;
ALTER TABLE businesses ADD CONSTRAINT businesses_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id);
