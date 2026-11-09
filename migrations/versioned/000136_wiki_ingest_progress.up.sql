-- 领取代次与任务 ID 一起构成旧 worker 的写入栅栏。
ALTER TABLE task_pending_ops ADD COLUMN claim_epoch BIGINT NOT NULL DEFAULT 0;

-- Map 与页面分配计划只写一次，逐页进度单独存储，避免反复改写大 JSONB。
CREATE TABLE wiki_ingest_plans (
    pending_op_id BIGINT PRIMARY KEY REFERENCES task_pending_ops(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(64) NOT NULL,
    knowledge_id VARCHAR(128) NOT NULL,
    op VARCHAR(32) NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    source_attempt INTEGER NOT NULL DEFAULT 0,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE wiki_ingest_page_progress (
    pending_op_id BIGINT NOT NULL REFERENCES task_pending_ops(id) ON DELETE CASCADE,
    slug VARCHAR(255) NOT NULL,
    applied BOOLEAN NOT NULL DEFAULT FALSE,
    effects_done BOOLEAN NOT NULL DEFAULT FALSE,
    page_id VARCHAR(64) NOT NULL DEFAULT '',
    page_version INTEGER NOT NULL DEFAULT 0,
    changed BOOLEAN NOT NULL DEFAULT FALSE,
    old_out_links JSONB NOT NULL DEFAULT '[]',
    new_out_links JSONB NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (pending_op_id, slug),
    CHECK (NOT effects_done OR applied)
);
