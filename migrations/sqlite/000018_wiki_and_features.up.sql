-- Sync Lite SQLite schema: wiki, indexing, processing spans, task queue, multi-tags.

ALTER TABLE knowledge_bases ADD COLUMN wiki_config TEXT DEFAULT NULL;
ALTER TABLE knowledge_bases ADD COLUMN indexing_strategy TEXT DEFAULT NULL;

UPDATE knowledge_bases
SET indexing_strategy = '{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}'
WHERE indexing_strategy IS NULL;

CREATE TABLE IF NOT EXISTS wiki_pages (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    title VARCHAR(512) NOT NULL DEFAULT '',
    page_type VARCHAR(32) NOT NULL DEFAULT 'summary',
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    content TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    parent_slug VARCHAR(255) NOT NULL DEFAULT '',
    folder_id VARCHAR(36) NOT NULL DEFAULT '',
    category_path TEXT DEFAULT '[]',
    wiki_path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    source_refs TEXT DEFAULT '[]',
    chunk_refs TEXT DEFAULT '[]',
    in_links TEXT DEFAULT '[]',
    out_links TEXT DEFAULT '[]',
    page_metadata TEXT DEFAULT '{}',
    aliases TEXT DEFAULT '[]',
    version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_pages_kb_slug
    ON wiki_pages (knowledge_base_id, slug)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_wiki_pages_kb_id ON wiki_pages (knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_page_type ON wiki_pages (knowledge_base_id, page_type);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_parent_slug ON wiki_pages (knowledge_base_id, parent_slug);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_tree
    ON wiki_pages (knowledge_base_id, page_type, wiki_path, sort_order, title);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_folder ON wiki_pages (knowledge_base_id, folder_id);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_tenant_id ON wiki_pages (tenant_id);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_deleted_at ON wiki_pages (deleted_at);

CREATE TABLE IF NOT EXISTS wiki_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_folders_parent_name
    ON wiki_folders (knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_wiki_folders_parent ON wiki_folders (knowledge_base_id, parent_id);
CREATE INDEX IF NOT EXISTS idx_wiki_folders_deleted_at ON wiki_folders (deleted_at);

CREATE TABLE IF NOT EXISTS wiki_page_issues (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    issue_type VARCHAR(50) NOT NULL,
    description TEXT NOT NULL,
    suspected_knowledge_ids TEXT,
    status VARCHAR(20) DEFAULT 'pending' NOT NULL,
    reported_by VARCHAR(100) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_tenant_id ON wiki_page_issues(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_knowledge_base_id ON wiki_page_issues(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_slug ON wiki_page_issues(slug);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_status ON wiki_page_issues(status);

CREATE TABLE IF NOT EXISTS wiki_log_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    action VARCHAR(32) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    doc_title TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    pages_affected TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_kb_id_desc
    ON wiki_log_entries (knowledge_base_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_tenant_id ON wiki_log_entries (tenant_id);

CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    knowledge_id VARCHAR(64) NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 1,
    span_id VARCHAR(64) NOT NULL,
    parent_span_id VARCHAR(64),
    name VARCHAR(64) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL,
    input TEXT,
    output TEXT,
    metadata TEXT,
    error_code VARCHAR(64),
    error_message TEXT,
    error_detail TEXT,
    started_at DATETIME,
    finished_at DATETIME,
    duration_ms INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (knowledge_id, attempt, span_id)
);

CREATE INDEX IF NOT EXISTS idx_kpspan_knowledge_attempt
    ON knowledge_processing_spans (knowledge_id, attempt);
CREATE INDEX IF NOT EXISTS idx_kpspan_status_started
    ON knowledge_processing_spans (status, started_at);
CREATE INDEX IF NOT EXISTS idx_kpspan_parent
    ON knowledge_processing_spans (parent_span_id)
    WHERE parent_span_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS task_pending_ops (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    op VARCHAR(32) NOT NULL,
    dedup_key VARCHAR(128) NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '{}',
    fail_count INTEGER NOT NULL DEFAULT 0,
    enqueued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_task_pending_ops_scope
    ON task_pending_ops (task_type, scope, scope_id, id);
CREATE INDEX IF NOT EXISTS idx_task_pending_ops_tenant ON task_pending_ops (tenant_id);

CREATE TABLE IF NOT EXISTS task_dead_letters (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    related_id VARCHAR(64) NOT NULL DEFAULT '',
    payload TEXT NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    fail_count INTEGER NOT NULL,
    failed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_task_dead_letters_scope
    ON task_dead_letters (scope, scope_id, failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_dead_letters_tenant
    ON task_dead_letters (tenant_id, failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_dead_letters_task_type
    ON task_dead_letters (task_type, failed_at DESC);

CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (knowledge_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_ktr_knowledge ON knowledge_tag_relations(knowledge_id);
CREATE INDEX IF NOT EXISTS idx_ktr_tag ON knowledge_tag_relations(tag_id);

INSERT OR IGNORE INTO knowledge_tag_relations (knowledge_id, tag_id, created_at)
SELECT id, tag_id, updated_at
FROM knowledges
WHERE tag_id IS NOT NULL AND tag_id != ''
  AND deleted_at IS NULL;
