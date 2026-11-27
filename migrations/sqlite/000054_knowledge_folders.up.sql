CREATE TABLE IF NOT EXISTS knowledge_folders (
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    folder_path VARCHAR(1024) NOT NULL,
    PRIMARY KEY (tenant_id, knowledge_base_id, folder_path)
);
