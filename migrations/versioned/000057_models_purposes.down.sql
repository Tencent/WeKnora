-- Rollback for 000057_models_purposes.
DO $$ BEGIN RAISE NOTICE '[Migration 000057] Dropping models.purposes'; END $$;

ALTER TABLE models
    DROP COLUMN IF EXISTS purposes;

DO $$ BEGIN RAISE NOTICE '[Migration 000057] models.purposes dropped'; END $$;
