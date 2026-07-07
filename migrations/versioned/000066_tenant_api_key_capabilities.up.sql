DO $$ BEGIN RAISE NOTICE '[Migration 000066] Adding tenant_api_keys.capabilities...'; END $$;

-- Additive per-key grants for non-full-access keys. Capabilities let a scoped
-- key call a bounded route family, while knowledge_base_ids still constrains
-- the KBs the key can touch where a route targets knowledge-base data.
ALTER TABLE tenant_api_keys
    ADD COLUMN IF NOT EXISTS capabilities JSONB NOT NULL DEFAULT '[]'::jsonb;

DO $$ BEGIN RAISE NOTICE '[Migration 000066] tenant_api_keys.capabilities ready'; END $$;
