-- Relative file references resolve a sibling by its complete logical path.
CREATE INDEX IF NOT EXISTS idx_knowledges_kb_logical_path
    ON knowledges (tenant_id, knowledge_base_id, folder_path, file_name);
