CREATE TABLE IF NOT EXISTS wiki_log_entries (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    action            VARCHAR(32) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL DEFAULT '',
    doc_title         TEXT NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '',
    pages_affected    TEXT NOT NULL DEFAULT '[]',
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_kb_id_desc
    ON wiki_log_entries (knowledge_base_id, id DESC);

CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_tenant_id
    ON wiki_log_entries (tenant_id);
