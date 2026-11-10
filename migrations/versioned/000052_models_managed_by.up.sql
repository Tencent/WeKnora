-- Migration: 000052_models_managed_by
-- Add a `managed_by` column to `models` so the YAML built-in models loader can
-- own a slice of the table without disturbing rows created via the UI/API or
-- seeded by hand-written SQL.
--
-- Background:
--   * 000051-era PR #1453 introduced config/builtin_models.yaml as a
--     declarative source of truth for built-in models. The first version
--     could add/update rows but had no way to remove them: deleting an entry
--     from YAML left the corresponding row in `models`, breaking the
--     "declarative" contract and forcing operators back into ad-hoc SQL.
--   * Indiscriminately deleting `is_builtin=true` rows on each startup is
--     unsafe because some deployments seed built-ins via direct SQL, and
--     those rows must not be touched.
--
-- Solution:
--   * Add `managed_by` (varchar(32), default ''). YAML loader writes "yaml";
--     anything else (UI / API / SQL seed) keeps the empty default.
--   * On every startup the loader UPSERTs YAML entries with managed_by='yaml'
--     and soft-deletes rows where (is_builtin=true AND managed_by='yaml' AND
--     id NOT IN <current YAML id set>). Manual rows are never inspected.
--
-- This migration is idempotent (IF NOT EXISTS) and safe to re-run.

DO $$ BEGIN RAISE NOTICE '[Migration 000052] Adding models.managed_by column'; END $$;

ALTER TABLE models
    ADD COLUMN IF NOT EXISTS managed_by VARCHAR(32) NOT NULL DEFAULT '';

-- A partial index on the YAML-managed slice keeps the startup sweep cheap
-- even if the table grows large. Manual rows (managed_by='') are excluded
-- so the index stays small.
CREATE INDEX IF NOT EXISTS idx_models_managed_by_yaml
    ON models (managed_by)
    WHERE managed_by <> '';

DO $$ BEGIN RAISE NOTICE '[Migration 000052] Done'; END $$;
