-- MySQL 8 translation of 000026_chunks_query_indexes.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE INDEX idx_chunks_kb_tenant ON chunks(knowledge_base_id, tenant_id);
CREATE INDEX idx_chunks_knowledge_enabled ON chunks(knowledge_id, is_enabled, deleted_at);
