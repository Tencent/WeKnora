DO $$ BEGIN RAISE NOTICE '[Migration 000120] Adding tenants.assistant_config...'; END $$;

-- Workspace setting of the WeKnora assistant: display name and the highest
-- access level a member may pick.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS assistant_config JSONB;
