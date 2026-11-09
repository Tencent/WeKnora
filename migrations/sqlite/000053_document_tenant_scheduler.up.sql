CREATE TABLE document_scheduler_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    last_tenant_id BIGINT NOT NULL DEFAULT 0
);
INSERT INTO document_scheduler_state (id, last_tenant_id) VALUES (1, 0);

-- No foreign keys: deleting queued work must not free a running worker's slot.
CREATE TABLE document_execution_leases (
    tenant_id BIGINT NOT NULL,
    knowledge_id VARCHAR(64) NOT NULL,
    pending_id BIGINT NOT NULL,
    owner VARCHAR(36) NOT NULL,
    expires_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, knowledge_id)
);
CREATE INDEX idx_document_execution_expiry ON document_execution_leases(expires_at);
ALTER TABLE task_pending_ops ADD COLUMN available_at DATETIME;
CREATE INDEX idx_document_pending_tenant ON task_pending_ops (tenant_id, available_at, id)
    WHERE task_type = 'document:process' AND scope = 'knowledge_base';
CREATE INDEX idx_document_terminal_attempt ON task_dead_letters (tenant_id, related_id) WHERE task_type = 'document:process';

-- Persistent producer tombstone: old unversioned queue deliveries cannot adopt a reparse.
CREATE TABLE document_generations (
    tenant_id BIGINT NOT NULL,
    knowledge_id VARCHAR(64) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    token VARCHAR(36) NOT NULL,
    advanced BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (tenant_id, knowledge_id)
);
ALTER TABLE task_pending_ops ADD COLUMN generation VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE task_dead_letters ADD COLUMN generation VARCHAR(36) NOT NULL DEFAULT '';
