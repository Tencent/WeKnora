-- MySQL 8 translation of 000041_task_queue_and_wiki_indexes.
CREATE TABLE task_pending_ops (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    op VARCHAR(32) NOT NULL,
    dedup_key VARCHAR(128) NOT NULL DEFAULT '',
    payload JSON NOT NULL,
    fail_count INT NOT NULL DEFAULT 0,
    enqueued_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_at TIMESTAMP NULL
);
CREATE INDEX idx_task_pending_ops_scope ON task_pending_ops(task_type, scope, scope_id, id);
CREATE INDEX idx_task_pending_ops_tenant ON task_pending_ops(tenant_id);

CREATE TABLE task_dead_letters (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    related_id VARCHAR(64) NOT NULL DEFAULT '',
    payload JSON NOT NULL,
    last_error TEXT NOT NULL,
    fail_count INT NOT NULL,
    failed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_task_dead_letters_scope ON task_dead_letters(scope, scope_id, failed_at DESC);
CREATE INDEX idx_task_dead_letters_tenant ON task_dead_letters(tenant_id, failed_at DESC);
CREATE INDEX idx_task_dead_letters_task_type ON task_dead_letters(task_type, failed_at DESC);

-- MySQL has neither GIN nor trigram indexes. The plain indexes preserve
-- equality/prefix lookup paths; JSON containment remains application-filtered.
CREATE INDEX idx_wiki_pages_title ON wiki_pages(title(128));
