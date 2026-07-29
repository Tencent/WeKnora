-- Deleted log rows cannot be reconstructed; restore only the former schema.
CREATE TABLE wiki_log_entries (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    action VARCHAR(32) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    doc_title TEXT NOT NULL DEFAULT (''),
    summary TEXT NOT NULL DEFAULT (''),
    pages_affected JSON NOT NULL DEFAULT (JSON_ARRAY()),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_wiki_log_entries_kb_id_desc (knowledge_base_id, id DESC),
    KEY idx_wiki_log_entries_tenant_id (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
