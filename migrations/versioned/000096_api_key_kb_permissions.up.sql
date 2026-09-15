-- Description: Per-knowledge-base capability overlay for scoped tenant API keys.
-- Empty object means every allow-listed KB inherits the key's global
-- retrieve / ingest / manage_kbs capabilities.
DO $$ BEGIN RAISE NOTICE '[Migration 000096] Adding tenant_api_keys.knowledge_base_permissions'; END $$;

ALTER TABLE tenant_api_keys
    ADD COLUMN IF NOT EXISTS knowledge_base_permissions JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN tenant_api_keys.knowledge_base_permissions IS
    'Per-KB capability subset (retrieve/ingest/manage_kbs). Empty object inherits the key global capabilities for every allow-listed knowledge base.';
