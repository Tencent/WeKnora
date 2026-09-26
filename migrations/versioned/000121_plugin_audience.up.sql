-- Migration 000121: which workspaces see an installed plugin. NULL means
-- every workspace; a JSON array of tenant IDs limits it to those.
DO $$ BEGIN RAISE NOTICE '[Migration 000121] Adding plugins.audience'; END $$;

ALTER TABLE plugins ADD COLUMN IF NOT EXISTS audience JSONB;
