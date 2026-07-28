-- Migration: 000078_content_addressed_rebuild_cache (rollback)

DROP TABLE IF EXISTS generation_caches;
DROP TABLE IF EXISTS embedding_caches;
