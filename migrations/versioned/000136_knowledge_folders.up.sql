-- Explicitly created document folders survive even without knowledge rows.
-- Existing uploaded folders continue to be derived from knowledges.folder_path.
CREATE TABLE IF NOT EXISTS knowledge_folders (
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, knowledge_base_id, path),
    CONSTRAINT knowledge_folders_nonempty_path CHECK (path <> ''),
    CONSTRAINT knowledge_folders_path_byte_length CHECK (octet_length(path) <= 1024)
);
