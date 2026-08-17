DROP TABLE IF EXISTS memory_item_embeddings;
DROP TABLE IF EXISTS memory_doc_affinity;
DROP TABLE IF EXISTS memory_topic_stats;
DROP TABLE IF EXISTS memory_items;
DROP TABLE IF EXISTS memory_tombstones;
DROP TABLE IF EXISTS memory_subjects;

ALTER TABLE messages DROP COLUMN used_memories;
ALTER TABLE tenants DROP COLUMN memory_config;
