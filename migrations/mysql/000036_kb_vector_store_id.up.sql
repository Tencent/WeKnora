-- MySQL 8 translation of 000036_kb_vector_store_id.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE knowledge_bases ADD COLUMN vector_store_id VARCHAR(36);
CREATE INDEX idx_knowledge_bases_tenant_vector_store ON knowledge_bases(tenant_id, vector_store_id);
