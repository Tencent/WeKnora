-- OAuth connections of plugin forms (versioned 000119).
CREATE TABLE IF NOT EXISTS plugin_oauth_connections (
    id           VARCHAR(36)   PRIMARY KEY,
    plugin_id    VARCHAR(128)  NOT NULL,
    tenant_id    BIGINT        NOT NULL,
    scope        VARCHAR(16)   NOT NULL,
    contribution VARCHAR(160)  NOT NULL DEFAULT '',
    field        VARCHAR(256)  NOT NULL,
    token        TEXT          NOT NULL,
    expires_at   DATETIME,
    created_by   VARCHAR(64)   NOT NULL DEFAULT '',
    created_at   DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_plugin_oauth_connections_owner ON plugin_oauth_connections (plugin_id, tenant_id);
