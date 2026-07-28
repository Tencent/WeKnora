-- MySQL 8 translation of 000037_wiki_and_indexing.
ALTER TABLE knowledge_bases ADD COLUMN wiki_config JSON;

CREATE TABLE wiki_pages (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    title VARCHAR(512) NOT NULL DEFAULT '',
    page_type VARCHAR(32) NOT NULL DEFAULT 'summary',
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    content TEXT NOT NULL,
    summary TEXT NOT NULL,
    parent_slug VARCHAR(255) NOT NULL DEFAULT '',
    folder_id VARCHAR(36) NOT NULL DEFAULT '',
    category_path JSON,
    wiki_path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    source_refs JSON,
    chunk_refs JSON,
    in_links JSON,
    out_links JSON,
    page_metadata JSON,
    aliases JSON,
    version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);
CREATE UNIQUE INDEX idx_wiki_pages_kb_slug ON wiki_pages(knowledge_base_id, slug);
CREATE INDEX idx_wiki_pages_kb_id ON wiki_pages(knowledge_base_id);
CREATE INDEX idx_wiki_pages_page_type ON wiki_pages(knowledge_base_id, page_type);
CREATE INDEX idx_wiki_pages_parent_slug ON wiki_pages(knowledge_base_id, parent_slug);
-- MySQL/InnoDB limits composite key length to 3072 bytes under utf8mb4.
-- Prefixes retain the tree-listing selectivity without exceeding that limit.
CREATE INDEX idx_wiki_pages_tree ON wiki_pages(knowledge_base_id, page_type, wiki_path(128), sort_order, title(128));
CREATE INDEX idx_wiki_pages_folder ON wiki_pages(knowledge_base_id, folder_id);
CREATE INDEX idx_wiki_pages_tenant_id ON wiki_pages(tenant_id);
CREATE INDEX idx_wiki_pages_deleted_at ON wiki_pages(deleted_at);
CREATE FULLTEXT INDEX idx_wiki_pages_fulltext ON wiki_pages(title, content);

CREATE TABLE wiki_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL DEFAULT '',
    depth INT NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);
CREATE UNIQUE INDEX idx_wiki_folders_parent_name ON wiki_folders(knowledge_base_id, parent_id, name);
CREATE INDEX idx_wiki_folders_parent ON wiki_folders(knowledge_base_id, parent_id);
CREATE INDEX idx_wiki_folders_deleted_at ON wiki_folders(deleted_at);

CREATE TABLE wiki_page_issues (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    issue_type VARCHAR(50) NOT NULL,
    description TEXT NOT NULL,
    suspected_knowledge_ids JSON,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    reported_by VARCHAR(100) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);
CREATE INDEX idx_wiki_page_issues_tenant_id ON wiki_page_issues(tenant_id);
CREATE INDEX idx_wiki_page_issues_knowledge_base_id ON wiki_page_issues(knowledge_base_id);
CREATE INDEX idx_wiki_page_issues_slug ON wiki_page_issues(slug);
CREATE INDEX idx_wiki_page_issues_status ON wiki_page_issues(status);

ALTER TABLE knowledge_bases ADD COLUMN indexing_strategy JSON;
UPDATE knowledge_bases
SET indexing_strategy = JSON_OBJECT('vector_enabled', TRUE, 'keyword_enabled', TRUE, 'wiki_enabled', FALSE, 'graph_enabled', FALSE)
WHERE indexing_strategy IS NULL;
