DROP INDEX IF EXISTS idx_knowledge_bases_storage_backend;
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS storage_backend_id;
ALTER TABLE tenants DROP COLUMN IF EXISTS default_storage_backend_id;
DROP TABLE IF EXISTS storage_backends;
