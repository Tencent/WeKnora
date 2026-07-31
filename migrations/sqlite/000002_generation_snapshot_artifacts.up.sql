ALTER TABLE knowledges ADD COLUMN active_generation_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_knowledges_active_generation ON knowledges(active_generation_id);

ALTER TABLE chunks ADD COLUMN generation_id VARCHAR(36);
ALTER TABLE chunks ADD COLUMN logical_chunk_key VARCHAR(64);
ALTER TABLE chunks ADD COLUMN artifact_digest VARCHAR(64);
UPDATE chunks
SET logical_chunk_key = id
WHERE (generation_id IS NULL OR generation_id = '')
  AND (logical_chunk_key IS NULL OR logical_chunk_key = '');
CREATE INDEX IF NOT EXISTS idx_chunks_active_generation
    ON chunks(tenant_id, knowledge_id, generation_id, chunk_type, chunk_index);
CREATE UNIQUE INDEX IF NOT EXISTS uk_chunks_generation_logical
    ON chunks(tenant_id, knowledge_id, generation_id, logical_chunk_key)
    WHERE generation_id IS NOT NULL AND logical_chunk_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS knowledge_generations (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           INTEGER NOT NULL,
    knowledge_id        VARCHAR(36) NOT NULL,
    attempt             INTEGER NOT NULL,
    base_generation_id  VARCHAR(36),
    state               VARCHAR(20) NOT NULL,
    source_digest       VARCHAR(64) NOT NULL,
    pipeline_digest     VARCHAR(64) NOT NULL,
    manifest_digest     VARCHAR(64),
    snapshot_description TEXT,
    error_message       TEXT,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ready_at            DATETIME,
    activated_at        DATETIME,
    retired_at          DATETIME,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, knowledge_id, attempt)
);
CREATE INDEX IF NOT EXISTS idx_knowledge_generations_lookup
    ON knowledge_generations(tenant_id, knowledge_id, state);

CREATE TABLE IF NOT EXISTS processing_artifacts (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           INTEGER NOT NULL,
    stage               VARCHAR(64) NOT NULL,
    key_version         INTEGER NOT NULL,
    artifact_key        VARCHAR(64) NOT NULL,
    processor_digest    VARCHAR(64) NOT NULL,
    output_digest       VARCHAR(64) NOT NULL,
    output_schema       VARCHAR(64) NOT NULL,
    codec               VARCHAR(20) NOT NULL,
    payload             BLOB,
    payload_checksum    VARCHAR(64) NOT NULL,
    payload_size        INTEGER NOT NULL,
    hit_count           INTEGER NOT NULL DEFAULT 0,
    last_hit_at         DATETIME,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at          DATETIME,
    UNIQUE (tenant_id, stage, key_version, artifact_key)
);
