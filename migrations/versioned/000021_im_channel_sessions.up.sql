-- Migration: 000021_im_channel_sessions
-- Description: Create IM channel-to-session mapping table
DO $$ BEGIN RAISE NOTICE '[Migration 000021] Creating table: im_channel_sessions'; END $$;

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

DO $$ BEGIN RAISE NOTICE '[Migration 000021] im_channel_sessions setup completed successfully!'; END $$;
