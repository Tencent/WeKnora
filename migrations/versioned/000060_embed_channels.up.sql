-- Migration: 000060_embed_channels
-- Web embed channels for publishing agent chat to external sites.
DO $$ BEGIN RAISE NOTICE '[Migration 000060] Creating embed_channels table'; END $$;

CREATE TABLE IF NOT EXISTS embed_channels (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    publish_token VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins JSONB NOT NULL DEFAULT '[]',
    welcome_message TEXT NOT NULL DEFAULT '',
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 30,
    rate_limit_per_day INTEGER NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT '',
    page_title VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions BOOLEAN NOT NULL DEFAULT true,
    widget_position VARCHAR(32) NOT NULL DEFAULT 'bottom-right',
    allow_web_search BOOLEAN NOT NULL DEFAULT false,
    allow_memory BOOLEAN NOT NULL DEFAULT false,
    allow_file_upload BOOLEAN NOT NULL DEFAULT false,
    default_locale VARCHAR(16) NOT NULL DEFAULT '',
    webhook_url VARCHAR(512) NOT NULL DEFAULT '',
    webhook_secret VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_embed_channels_tenant ON embed_channels (tenant_id);
CREATE INDEX IF NOT EXISTS idx_embed_channels_agent ON embed_channels (agent_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_embed_channels_publish_token
    ON embed_channels (publish_token)
    WHERE publish_token <> '' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_embed_channels_deleted ON embed_channels (deleted_at) WHERE deleted_at IS NOT NULL;

COMMENT ON TABLE embed_channels IS 'Web embed channels for publishing agent chat to external sites via iframe';
COMMENT ON COLUMN embed_channels.publish_token IS 'Plaintext scoped token (em_ prefix); rotatable from management UI';
COMMENT ON COLUMN embed_channels.allowed_origins IS 'JSON array of allowed HTTP(S) origins for embed API requests; empty rejects all (management UI requires at least one)';
COMMENT ON COLUMN embed_channels.rate_limit_per_minute IS 'Per-IP per-minute request cap for the public embed endpoints';
COMMENT ON COLUMN embed_channels.rate_limit_per_day IS
    'Channel-wide daily total request cap across all IPs; bounds cost/abuse since the publish token is publicly visible';
COMMENT ON COLUMN embed_channels.primary_color IS 'CSS color for embed widget accent (e.g. #0052d9)';
COMMENT ON COLUMN embed_channels.page_title IS 'Browser tab title for the embed page';
COMMENT ON COLUMN embed_channels.header_title_mode IS
    'Embed header title source: channel (fixed page title) or session (auto-generated after first message)';
COMMENT ON COLUMN embed_channels.show_suggested_questions IS
    'When true, embed chat shows suggested starter questions before the first visitor message';
COMMENT ON COLUMN embed_channels.widget_position IS 'Floating widget corner: bottom-right, bottom-left, top-right, top-left';
COMMENT ON COLUMN embed_channels.allow_web_search IS
    'When true, embed chat may show web search toggle; visitor must opt in per message';
COMMENT ON COLUMN embed_channels.allow_memory IS
    'When true, embed chat may use agent memory; client cannot override when false';
COMMENT ON COLUMN embed_channels.allow_file_upload IS
    'When true, embed chat may send images; client cannot override when false';
COMMENT ON COLUMN embed_channels.default_locale IS
    'Default visitor UI locale (zh-CN, en-US, ko-KR, ru-RU); empty follows browser';
COMMENT ON COLUMN embed_channels.webhook_url IS 'HTTPS endpoint for outbound message_sent / message_received events';
COMMENT ON COLUMN embed_channels.webhook_secret IS 'Optional HMAC-SHA256 secret for X-WeKnora-Signature header';

DO $$ BEGIN RAISE NOTICE '[Migration 000060] embed_channels created'; END $$;
