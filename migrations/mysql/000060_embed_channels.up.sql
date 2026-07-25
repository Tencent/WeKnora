-- MySQL 8 translation of 000060_embed_channels.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE TABLE embed_channels (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT 'builtin-quick-answer',
    name VARCHAR(255) NOT NULL DEFAULT '',
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    publish_token VARCHAR(64) NOT NULL DEFAULT '',
    allowed_origins JSON NOT NULL,
    welcome_message TEXT NOT NULL,
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 30,
    rate_limit_per_day INTEGER NOT NULL DEFAULT 10000,
    primary_color VARCHAR(32) NOT NULL DEFAULT '',
    page_title VARCHAR(255) NOT NULL DEFAULT '',
    header_title_mode VARCHAR(32) NOT NULL DEFAULT 'channel',
    show_suggested_questions TINYINT(1) NOT NULL DEFAULT 1,
    widget_position VARCHAR(32) NOT NULL DEFAULT 'bottom-right',
    allow_web_search TINYINT(1) NOT NULL DEFAULT 0,
    allow_memory TINYINT(1) NOT NULL DEFAULT 0,
    allow_file_upload TINYINT(1) NOT NULL DEFAULT 0,
    default_locale VARCHAR(16) NOT NULL DEFAULT '',
    webhook_url VARCHAR(512) NOT NULL DEFAULT '',
    webhook_secret VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);
CREATE INDEX idx_embed_channels_tenant ON embed_channels (tenant_id);
CREATE INDEX idx_embed_channels_agent ON embed_channels (agent_id);
CREATE INDEX idx_embed_channels_deleted ON embed_channels (deleted_at);
