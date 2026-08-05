-- Generic deferred-sync checkpoint plus OneDrive OAuth and drive-item state.
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Creating OneDrive data source state'; END $$;

ALTER TABLE data_sources
    ADD COLUMN IF NOT EXISTS connection_version BIGINT NOT NULL DEFAULT 1;

ALTER TABLE sync_logs
    ADD COLUMN IF NOT EXISTS checkpoint JSONB;

CREATE TABLE IF NOT EXISTS data_source_oauth_tokens (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    data_source_id VARCHAR(36) NOT NULL REFERENCES data_sources(id) ON DELETE CASCADE,
    provider VARCHAR(32) NOT NULL,
    access_token TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    token_type VARCHAR(32),
    scopes TEXT,
    expires_at TIMESTAMP NOT NULL,
    provider_account_id VARCHAR(255) NOT NULL,
    provider_tenant_id VARCHAR(255),
    authorized_drive_id VARCHAR(255) NOT NULL,
    account_display_name VARCHAR(255),
    authorized_by_user_id VARCHAR(255) NOT NULL,
    connection_version BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ds_oauth_token_tenant_source
    ON data_source_oauth_tokens(tenant_id, data_source_id);
CREATE INDEX IF NOT EXISTS idx_ds_oauth_token_source
    ON data_source_oauth_tokens(data_source_id);

CREATE TABLE IF NOT EXISTS data_source_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    data_source_id VARCHAR(36) NOT NULL REFERENCES data_sources(id) ON DELETE CASCADE,
    connection_version BIGINT NOT NULL,
    drive_id VARCHAR(255) NOT NULL,
    item_id VARCHAR(255) NOT NULL,
    parent_item_id VARCHAR(255),
    item_type VARCHAR(16) NOT NULL,
    selected_root_id TEXT,
    external_id TEXT,
    last_modified_at TIMESTAMP,
    last_seen_generation VARCHAR(36),
    ingested BOOLEAN NOT NULL DEFAULT false,
    deleted_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ds_item_identity
    ON data_source_items(data_source_id, connection_version, drive_id, item_id);
CREATE INDEX IF NOT EXISTS idx_ds_item_scope
    ON data_source_items(tenant_id, data_source_id, connection_version);
CREATE INDEX IF NOT EXISTS idx_ds_item_parent
    ON data_source_items(data_source_id, connection_version, parent_item_id);
CREATE INDEX IF NOT EXISTS idx_ds_item_selected_root
    ON data_source_items(data_source_id, connection_version, selected_root_id);
CREATE INDEX IF NOT EXISTS idx_ds_item_generation
    ON data_source_items(data_source_id, connection_version, last_seen_generation);

DO $$ BEGIN RAISE NOTICE '[Migration 000071] OneDrive data source state ready'; END $$;
