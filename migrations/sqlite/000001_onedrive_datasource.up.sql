ALTER TABLE data_sources ADD COLUMN connection_version INTEGER NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS data_source_oauth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    data_source_id VARCHAR(36) NOT NULL REFERENCES data_sources(id) ON DELETE CASCADE,
    provider VARCHAR(32) NOT NULL,
    access_token TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    token_type VARCHAR(32),
    scopes TEXT,
    expires_at DATETIME NOT NULL,
    provider_account_id VARCHAR(255) NOT NULL,
    provider_tenant_id VARCHAR(255),
    authorized_drive_id VARCHAR(255) NOT NULL,
    account_display_name VARCHAR(255),
    authorized_by_user_id VARCHAR(255) NOT NULL,
    connection_version INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, data_source_id)
);

CREATE INDEX IF NOT EXISTS idx_ds_oauth_token_source
    ON data_source_oauth_tokens(data_source_id);

CREATE TABLE IF NOT EXISTS data_source_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    data_source_id VARCHAR(36) NOT NULL REFERENCES data_sources(id) ON DELETE CASCADE,
    connection_version INTEGER NOT NULL,
    drive_id VARCHAR(255) NOT NULL,
    item_id VARCHAR(255) NOT NULL,
    parent_item_id VARCHAR(255),
    item_type VARCHAR(16) NOT NULL,
    selected_root_id TEXT,
    external_id TEXT,
    last_modified_at DATETIME,
    last_seen_generation VARCHAR(36),
    ingested INTEGER NOT NULL DEFAULT 0,
    deleted_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(data_source_id, connection_version, drive_id, item_id)
);

CREATE INDEX IF NOT EXISTS idx_ds_item_scope
    ON data_source_items(tenant_id, data_source_id, connection_version);
CREATE INDEX IF NOT EXISTS idx_ds_item_parent
    ON data_source_items(data_source_id, connection_version, parent_item_id);
CREATE INDEX IF NOT EXISTS idx_ds_item_selected_root
    ON data_source_items(data_source_id, connection_version, selected_root_id);
CREATE INDEX IF NOT EXISTS idx_ds_item_generation
    ON data_source_items(data_source_id, connection_version, last_seen_generation);
