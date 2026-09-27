-- Owner and trusted signer of uninstalled plugins (versioned 000124).
CREATE TABLE IF NOT EXISTS plugin_tombstones (
    plugin_id       VARCHAR(128) PRIMARY KEY,
    owner_tenant_id BIGINT,
    signer_key_id   VARCHAR(128) NOT NULL DEFAULT '',
    created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
);
