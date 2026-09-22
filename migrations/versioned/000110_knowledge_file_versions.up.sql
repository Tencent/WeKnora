ALTER TABLE knowledges ADD COLUMN file_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE knowledges ADD COLUMN file_version_created_at TIMESTAMPTZ;
CREATE TABLE knowledge_file_versions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    version INTEGER NOT NULL,
    file_name TEXT NOT NULL,
    file_type TEXT NOT NULL,
    file_size BIGINT NOT NULL,
    file_hash TEXT NOT NULL,
    file_path TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX idx_knowledge_file_version ON knowledge_file_versions (knowledge_id, version);
CREATE INDEX idx_knowledge_file_versions_tenant_id ON knowledge_file_versions (tenant_id);
