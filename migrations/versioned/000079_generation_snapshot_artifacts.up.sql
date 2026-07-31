-- Migration 000079: generation snapshots and reusable processing artifacts.
ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS active_generation_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_knowledges_active_generation ON knowledges(active_generation_id);

ALTER TABLE chunks ADD COLUMN IF NOT EXISTS generation_id VARCHAR(36);
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS logical_chunk_key VARCHAR(64);
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS artifact_digest VARCHAR(64);
UPDATE chunks
SET logical_chunk_key = id
WHERE (generation_id IS NULL OR generation_id = '')
  AND (logical_chunk_key IS NULL OR logical_chunk_key = '');
CREATE INDEX IF NOT EXISTS idx_chunks_active_generation
    ON chunks(tenant_id, knowledge_id, generation_id, chunk_type, chunk_index);
CREATE UNIQUE INDEX IF NOT EXISTS uk_chunks_generation_logical
    ON chunks(tenant_id, knowledge_id, generation_id, logical_chunk_key)
    WHERE generation_id IS NOT NULL AND logical_chunk_key IS NOT NULL;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'embeddings') THEN
        ALTER TABLE embeddings ADD COLUMN IF NOT EXISTS generation_id VARCHAR(36);
        ALTER TABLE embeddings ADD COLUMN IF NOT EXISTS visibility_key VARCHAR(128);
        CREATE INDEX IF NOT EXISTS idx_embeddings_generation_id ON embeddings(generation_id);
        CREATE INDEX IF NOT EXISTS idx_embeddings_visibility_key ON embeddings(visibility_key);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS knowledge_generations (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    knowledge_id        VARCHAR(36) NOT NULL,
    attempt             INTEGER NOT NULL,
    base_generation_id  VARCHAR(36),
    state               VARCHAR(20) NOT NULL,
    source_digest       VARCHAR(64) NOT NULL,
    pipeline_digest     VARCHAR(64) NOT NULL,
    manifest_digest     VARCHAR(64),
    snapshot_description TEXT,
    error_message       TEXT,
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    ready_at            TIMESTAMP WITH TIME ZONE,
    activated_at        TIMESTAMP WITH TIME ZONE,
    retired_at          TIMESTAMP WITH TIME ZONE,
    updated_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, knowledge_id, attempt)
);
CREATE INDEX IF NOT EXISTS idx_knowledge_generations_lookup
    ON knowledge_generations(tenant_id, knowledge_id, state);

CREATE TABLE IF NOT EXISTS processing_artifacts (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    stage               VARCHAR(64) NOT NULL,
    key_version         INTEGER NOT NULL,
    artifact_key        VARCHAR(64) NOT NULL,
    processor_digest    VARCHAR(64) NOT NULL,
    output_digest       VARCHAR(64) NOT NULL,
    output_schema       VARCHAR(64) NOT NULL,
    codec               VARCHAR(20) NOT NULL,
    payload             BYTEA,
    payload_checksum    VARCHAR(64) NOT NULL,
    payload_size        BIGINT NOT NULL,
    hit_count           BIGINT NOT NULL DEFAULT 0,
    last_hit_at         TIMESTAMP WITH TIME ZONE,
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMP WITH TIME ZONE,
    UNIQUE (tenant_id, stage, key_version, artifact_key)
);
