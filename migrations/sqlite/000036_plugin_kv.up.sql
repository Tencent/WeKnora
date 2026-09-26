-- Host API key-value store of plugins (versioned 000117).
CREATE TABLE IF NOT EXISTS plugin_kv (
    plugin_id  VARCHAR(128) NOT NULL,
    tenant_id  BIGINT       NOT NULL,
    key        VARCHAR(256) NOT NULL,
    value      TEXT         NOT NULL,
    expires_at DATETIME,
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_id, tenant_id, key)
);

CREATE INDEX IF NOT EXISTS idx_plugin_kv_expires_at ON plugin_kv (expires_at);
