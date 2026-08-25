-- File knowledge update coordination slot.
CREATE TABLE IF NOT EXISTS knowledge_file_update_slots (
    knowledge_id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    latest_version INTEGER NOT NULL DEFAULT 0,
    active_version INTEGER,
    active_state VARCHAR(16) NOT NULL DEFAULT 'idle',
    active_payload TEXT,
    pending_version INTEGER,
    pending_payload TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (active_state IN ('idle', 'waiting', 'applying', 'retry_wait', 'failed')),
    CHECK ((active_state = 'idle') = (active_version IS NULL)),
    CHECK ((active_version IS NULL) = (active_payload IS NULL)),
    CHECK ((pending_version IS NULL) = (pending_payload IS NULL))
);

CREATE INDEX IF NOT EXISTS idx_knowledge_file_update_slots_tenant_kb
    ON knowledge_file_update_slots(tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_file_update_slots_state
    ON knowledge_file_update_slots(active_state, updated_at);
