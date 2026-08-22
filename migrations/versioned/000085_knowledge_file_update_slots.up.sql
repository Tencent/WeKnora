DO $$ BEGIN RAISE NOTICE '[Migration 000085] Creating knowledge file update slots...'; END $$;

CREATE TABLE IF NOT EXISTS knowledge_file_update_slots (
    knowledge_id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    latest_version BIGINT NOT NULL DEFAULT 0,
    active_version BIGINT,
    active_state VARCHAR(16) NOT NULL DEFAULT 'idle',
    active_payload JSONB,
    pending_version BIGINT,
    pending_payload JSONB,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_knowledge_file_update_active_state
        CHECK (active_state IN ('idle', 'waiting', 'applying', 'retry_wait', 'failed')),
    CONSTRAINT chk_knowledge_file_update_active_idle
        CHECK ((active_state = 'idle') = (active_version IS NULL)),
    CONSTRAINT chk_knowledge_file_update_active_payload
        CHECK ((active_version IS NULL) = (active_payload IS NULL)),
    CONSTRAINT chk_knowledge_file_update_pending_payload
        CHECK ((pending_version IS NULL) = (pending_payload IS NULL))
);

COMMENT ON TABLE knowledge_file_update_slots IS '文件知识更新协调槽';

CREATE INDEX IF NOT EXISTS idx_knowledge_file_update_slots_tenant_kb
    ON knowledge_file_update_slots(tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_file_update_slots_state
    ON knowledge_file_update_slots(active_state, updated_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000085] Knowledge file update slots ready'; END $$;
