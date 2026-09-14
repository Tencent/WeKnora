-- Migration: 000082_guest_link_channels
DO $$ BEGIN RAISE NOTICE '[Migration 000082] Creating guest_link_channels'; END $$;

CREATE TABLE IF NOT EXISTS guest_link_channels (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    web_slug VARCHAR(16) NOT NULL DEFAULT '',
    welcome_message TEXT NOT NULL DEFAULT '',
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 30,
    rate_limit_per_day INTEGER NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT '',
    page_title VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions BOOLEAN NOT NULL DEFAULT true,
    allow_web_search BOOLEAN NOT NULL DEFAULT false,
    allow_file_upload BOOLEAN NOT NULL DEFAULT false,
    default_locale VARCHAR(16) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_guest_link_channels_tenant
    ON guest_link_channels (tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_guest_link_channels_agent_unique
    ON guest_link_channels (tenant_id, agent_id)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_guest_link_channels_web_slug
    ON guest_link_channels (web_slug)
    WHERE web_slug <> '' AND deleted_at IS NULL;

COMMENT ON TABLE guest_link_channels IS
    'Per-agent login-free short-link chat window (/w/:slug); at most one per agent';

DO $$ BEGIN RAISE NOTICE '[Migration 000082] done'; END $$;
