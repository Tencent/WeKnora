CREATE TABLE IF NOT EXISTS wiki_log_entries (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    action VARCHAR(32) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    doc_title TEXT NOT NULL DEFAULT (''),
    summary TEXT NOT NULL DEFAULT (''),
    pages_affected JSON NOT NULL DEFAULT (JSON_ARRAY()),
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_wiki_log_entries_kb_id_desc
    ON wiki_log_entries(knowledge_base_id, id DESC);

CREATE INDEX idx_wiki_log_entries_tenant_id
    ON wiki_log_entries(tenant_id);
