-- Independent image-vector jobs; source liveness is checked against chunk_images.
CREATE TABLE IF NOT EXISTS image_vector_jobs (
 id VARCHAR(36) PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 knowledge_base_id VARCHAR(36) NOT NULL,
 knowledge_id VARCHAR(36) NOT NULL,
 source_chunk_id VARCHAR(36) NOT NULL,
 image_url TEXT NOT NULL,
 fingerprint VARCHAR(64) NOT NULL,
 chunk_id VARCHAR(36) NOT NULL,
 status VARCHAR(20) NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 lease_token VARCHAR(36) NOT NULL DEFAULT '',
 lease_until TIMESTAMP NOT NULL,
 created_at TIMESTAMP NOT NULL,
 updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_image_vector_jobs_scope
 ON image_vector_jobs(tenant_id, knowledge_base_id, knowledge_id);
