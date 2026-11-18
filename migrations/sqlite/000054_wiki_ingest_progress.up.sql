-- 与 PostgreSQL 共用领取代次和级联清理语义。
ALTER TABLE task_pending_ops ADD COLUMN claim_epoch INTEGER NOT NULL DEFAULT 0;

CREATE TABLE wiki_ingest_plans (
    pending_op_id INTEGER PRIMARY KEY REFERENCES task_pending_ops(id) ON DELETE CASCADE,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(64) NOT NULL,
    knowledge_id VARCHAR(128) NOT NULL,
    op VARCHAR(32) NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    source_attempt INTEGER NOT NULL DEFAULT 0,
    payload TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE wiki_ingest_page_progress (
    pending_op_id INTEGER NOT NULL REFERENCES task_pending_ops(id) ON DELETE CASCADE,
    slug VARCHAR(255) NOT NULL,
    applied BOOLEAN NOT NULL DEFAULT FALSE,
    effects_done BOOLEAN NOT NULL DEFAULT FALSE,
    page_id VARCHAR(64) NOT NULL DEFAULT '',
    page_version INTEGER NOT NULL DEFAULT 0,
    changed BOOLEAN NOT NULL DEFAULT FALSE,
    old_out_links TEXT NOT NULL DEFAULT '[]',
    new_out_links TEXT NOT NULL DEFAULT '[]',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (pending_op_id, slug),
    CHECK (NOT effects_done OR applied)
);
