-- Per-tenant plugin switches and configuration (versioned 000115).
CREATE TABLE IF NOT EXISTS plugin_tenant_settings (
    tenant_id  BIGINT       NOT NULL,
    plugin_id  VARCHAR(128) NOT NULL,
    enabled    BOOLEAN      NOT NULL DEFAULT 1,
    config     TEXT,
    updated_by VARCHAR(36)  NOT NULL DEFAULT '',
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, plugin_id)
);
