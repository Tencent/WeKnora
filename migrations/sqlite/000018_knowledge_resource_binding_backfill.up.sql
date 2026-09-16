-- Mirrors versioned migration 000097_knowledge_resource_binding_backfill.

CREATE TABLE IF NOT EXISTS data_patches (
    id TEXT PRIMARY KEY,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
