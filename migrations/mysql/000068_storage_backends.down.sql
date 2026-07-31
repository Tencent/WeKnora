DROP INDEX idx_knowledge_bases_storage_backend ON knowledge_bases;

ALTER TABLE knowledge_bases
    DROP COLUMN storage_backend_id;

ALTER TABLE tenants
    DROP COLUMN default_storage_backend_id;

DROP TABLE IF EXISTS storage_backends;
