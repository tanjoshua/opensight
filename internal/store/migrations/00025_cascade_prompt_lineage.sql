-- +goose Up
-- 00024 cascaded the two FKs into accounts, but deleting an account still
-- failed on any workspace that had ever run: prompt_results.prompt_id and
-- prompts.replaces_prompt_id restricted, and PostgreSQL does not order
-- cascade paths so that runs are cleared before the prompts they cite.
--
-- A result cannot outlive the prompt it answered, so it cascades. A lineage
-- pointer can: replaces_prompt_id is nullable and only records which prompt
-- this one superseded, so it is severed rather than propagating a delete
-- forward onto a prompt that is still active.
ALTER TABLE prompt_results DROP CONSTRAINT prompt_results_prompt_id_fkey;
ALTER TABLE prompt_results ADD CONSTRAINT prompt_results_prompt_id_fkey
  FOREIGN KEY (prompt_id) REFERENCES prompts(id) ON DELETE CASCADE;

ALTER TABLE prompts DROP CONSTRAINT prompts_replaces_prompt_id_fkey;
ALTER TABLE prompts ADD CONSTRAINT prompts_replaces_prompt_id_fkey
  FOREIGN KEY (replaces_prompt_id) REFERENCES prompts(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE prompts DROP CONSTRAINT prompts_replaces_prompt_id_fkey;
ALTER TABLE prompts ADD CONSTRAINT prompts_replaces_prompt_id_fkey
  FOREIGN KEY (replaces_prompt_id) REFERENCES prompts(id);

ALTER TABLE prompt_results DROP CONSTRAINT prompt_results_prompt_id_fkey;
ALTER TABLE prompt_results ADD CONSTRAINT prompt_results_prompt_id_fkey
  FOREIGN KEY (prompt_id) REFERENCES prompts(id);
