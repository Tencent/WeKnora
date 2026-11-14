DO $$ BEGIN RAISE NOTICE '[Migration 000134] Adding tenants.default_settings...'; END $$;

-- Workspace default model for each type and purpose. Empty slots fall back
-- to the is_default flag on model rows.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS default_settings JSONB;
