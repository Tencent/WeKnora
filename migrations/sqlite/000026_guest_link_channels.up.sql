-- Migration: 000010_guest_link_channels
-- Per-agent login-free short-link chat window (/w/:slug); at most one per agent.
-- (align with versioned 000082)

CREATE TABLE IF NOT EXISTS guest_link_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    agent_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    web_slug TEXT NOT NULL DEFAULT '',
    welcome_message TEXT NOT NULL DEFAULT '',
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 30,
    rate_limit_per_day INTEGER NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT '',
    page_title VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions INTEGER NOT NULL DEFAULT 1,
    allow_web_search INTEGER NOT NULL DEFAULT 0,
    allow_file_upload INTEGER NOT NULL DEFAULT 0,
    default_locale VARCHAR(16) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_guest_link_channels_tenant
    ON guest_link_channels (tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_guest_link_channels_agent_unique
    ON guest_link_channels (tenant_id, agent_id)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_guest_link_channels_web_slug
    ON guest_link_channels (web_slug)
    WHERE web_slug != '' AND deleted_at IS NULL;
