-- Migration 000075: wiki page revision history and edit provenance.
ALTER TABLE wiki_pages
    ADD COLUMN last_edit_source VARCHAR(16) NOT NULL DEFAULT '',
    ADD COLUMN last_editor_id VARCHAR(64) NOT NULL DEFAULT '';

CREATE TABLE wiki_page_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    page_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    version INT NOT NULL,
    title VARCHAR(512) NOT NULL DEFAULT '',
    page_type VARCHAR(32) NOT NULL DEFAULT 'summary',
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    content TEXT NOT NULL DEFAULT (''),
    summary TEXT NOT NULL DEFAULT (''),
    aliases JSON DEFAULT (JSON_ARRAY()),
    edit_source VARCHAR(16) NOT NULL DEFAULT '',
    editor_id VARCHAR(64) NOT NULL DEFAULT '',
    edited_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY idx_wiki_page_revisions_page_version (page_id, version),
    KEY idx_wiki_page_revisions_kb_slug (knowledge_base_id, slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
