-- Migration: 000060_embed_channels
-- Web embed channels for publishing knowledge-base chat to third-party sites.
DO $$ BEGIN RAISE NOTICE '[Migration 000060] Creating embed_channels table'; END $$;

CREATE TABLE IF NOT EXISTS embed_channels (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    publish_token VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins JSONB NOT NULL DEFAULT '[]',
    welcome_message TEXT NOT NULL DEFAULT '',
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 30,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_embed_channels_tenant ON embed_channels (tenant_id);
CREATE INDEX IF NOT EXISTS idx_embed_channels_kb ON embed_channels (knowledge_base_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_embed_channels_publish_token
    ON embed_channels (publish_token)
    WHERE publish_token <> '' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_embed_channels_deleted ON embed_channels (deleted_at) WHERE deleted_at IS NOT NULL;

COMMENT ON TABLE embed_channels IS 'Web embed channels for publishing KB chat to external sites via iframe';
COMMENT ON COLUMN embed_channels.publish_token IS 'Plaintext scoped token (em_ prefix); rotatable from management UI';
COMMENT ON COLUMN embed_channels.allowed_origins IS 'JSON array of allowed parent origins; empty means allow all';

DO $$ BEGIN RAISE NOTICE '[Migration 000060] embed_channels created'; END $$;
