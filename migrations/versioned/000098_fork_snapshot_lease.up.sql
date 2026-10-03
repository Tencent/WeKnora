CREATE TABLE IF NOT EXISTS fork_snapshot_leases (
    snapshot_id VARCHAR(128) PRIMARY KEY,
    tenant_id BIGINT NOT NULL DEFAULT 0,
    sandbox_config_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_fork_snapshot_leases_created_at
    ON fork_snapshot_leases (created_at);
