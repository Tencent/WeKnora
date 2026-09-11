CREATE INDEX IF NOT EXISTS idx_knowledges_kb_metadata_external_id
ON knowledges (knowledge_base_id, (metadata->>'external_id'))
WHERE deleted_at IS NULL;
