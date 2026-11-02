-- Explicitly created document folders; no placeholder knowledge is needed.
CREATE TABLE IF NOT EXISTS knowledge_folders (
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, knowledge_base_id, path),
    CONSTRAINT knowledge_folders_nonempty_path CHECK (path <> ''),
    CONSTRAINT knowledge_folders_path_byte_length CHECK (length(CAST(path AS BLOB)) <= 1024)
);
