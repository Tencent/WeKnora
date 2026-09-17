-- Mirrors versioned migration 000098_fork_snapshot_lease:
-- durable snapshot IDs taken before the forked session row is committed.

CREATE TABLE IF NOT EXISTS fork_snapshot_leases (
    snapshot_id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL DEFAULT 0,
    sandbox_config_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fork_snapshot_leases_created_at
    ON fork_snapshot_leases (created_at);
