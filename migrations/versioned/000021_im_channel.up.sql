-- Migration: 000021_im_channel_sessions
-- Description: Create IM channel session mapping and IM channel configuration tables
DO $$ BEGIN RAISE NOTICE '[Migration 000021] Creating IM channel integration tables'; END $$;

CREATE TABLE IF NOT EXISTS im_channel_sessions (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    platform VARCHAR(20) NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    chat_id VARCHAR(128) NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Partial unique index: only enforce uniqueness for non-deleted rows
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id)
    WHERE deleted_at IS NULL;

-- Index for tenant-based queries
CREATE INDEX IF NOT EXISTS idx_im_channel_tenant ON im_channel_sessions (tenant_id);

-- Index for session-based queries
CREATE INDEX IF NOT EXISTS idx_im_channel_session ON im_channel_sessions (session_id);

-- Partial index for soft deletes (only index deleted rows)
CREATE INDEX IF NOT EXISTS idx_im_channel_deleted ON im_channel_sessions (deleted_at) WHERE deleted_at IS NOT NULL;

COMMENT ON TABLE im_channel_sessions IS 'Maps IM platform channels to WeKnora conversation sessions';
COMMENT ON COLUMN im_channel_sessions.platform IS 'IM platform identifier: wecom, feishu, etc.';
COMMENT ON COLUMN im_channel_sessions.user_id IS 'Platform-specific user identifier';
COMMENT ON COLUMN im_channel_sessions.chat_id IS 'Platform-specific chat/group identifier, empty for direct messages';
COMMENT ON COLUMN im_channel_sessions.session_id IS 'Associated WeKnora session ID';
COMMENT ON COLUMN im_channel_sessions.tenant_id IS 'Tenant that owns this channel mapping';
COMMENT ON COLUMN im_channel_sessions.agent_id IS 'Custom agent ID used for this channel, empty for default';
COMMENT ON COLUMN im_channel_sessions.status IS 'Channel status: active, paused, expired';
COMMENT ON COLUMN im_channel_sessions.metadata IS 'Platform-specific extra data (JSON)';

DO $$ BEGIN RAISE NOTICE '[Migration 000021] Creating table: im_channels'; END $$;

CREATE TABLE IF NOT EXISTS im_channels (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    platform VARCHAR(20) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    mode VARCHAR(20) NOT NULL DEFAULT 'websocket',
    output_mode VARCHAR(20) NOT NULL DEFAULT 'stream',
    credentials JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_im_channels_tenant ON im_channels (tenant_id);
CREATE INDEX IF NOT EXISTS idx_im_channels_agent ON im_channels (agent_id);
CREATE INDEX IF NOT EXISTS idx_im_channels_deleted ON im_channels (deleted_at) WHERE deleted_at IS NOT NULL;

COMMENT ON TABLE im_channels IS 'IM platform channel configurations bound to agents';
COMMENT ON COLUMN im_channels.agent_id IS 'Agent ID this channel is bound to';
COMMENT ON COLUMN im_channels.platform IS 'IM platform: wecom, feishu';
COMMENT ON COLUMN im_channels.name IS 'User-defined channel name for identification';
COMMENT ON COLUMN im_channels.mode IS 'Connection mode: webhook or websocket';
COMMENT ON COLUMN im_channels.output_mode IS 'Output mode: stream (real-time) or full (wait for complete answer)';
COMMENT ON COLUMN im_channels.credentials IS 'Platform credentials (JSONB): WeCom webhook={corp_id,agent_secret,token,encoding_aes_key,corp_agent_id}, WeCom ws={bot_id,bot_secret}, Feishu={app_id,app_secret,verification_token,encrypt_key}';

-- Add im_channel_id column to im_channel_sessions for linking
ALTER TABLE im_channel_sessions ADD COLUMN IF NOT EXISTS im_channel_id VARCHAR(36) DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_im_channel_sessions_channel ON im_channel_sessions (im_channel_id) WHERE im_channel_id != '';

DO $$ BEGIN RAISE NOTICE '[Migration 000021] IM channel integration setup completed successfully!'; END $$;
