DO $$ BEGIN RAISE NOTICE '[Migration 000065] Adding tenant API principal config...'; END $$;

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS api_principal_config JSONB;

DO $$ BEGIN RAISE NOTICE '[Migration 000065] tenant API principal config ready'; END $$;
