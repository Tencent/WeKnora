-- Neighbour-spread credit for the knowledge-guidance overlay (Lite).
-- Mirrors versioned 000110.

CREATE TABLE IF NOT EXISTS memory_spread_views (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    slug              VARCHAR(255) NOT NULL,
    event_date        TEXT NOT NULL,
    spread_seconds    INTEGER NOT NULL DEFAULT 0,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_spread_scope
    ON memory_spread_views (tenant_id, subject_id, knowledge_base_id, slug, event_date);
