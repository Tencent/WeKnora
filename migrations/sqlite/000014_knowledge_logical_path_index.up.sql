-- Mirrors migrations/versioned/000092_knowledge_logical_path_index.up.sql.
CREATE INDEX IF NOT EXISTS idx_knowledges_kb_logical_path
    ON knowledges (tenant_id, knowledge_base_id, folder_path, file_name);
