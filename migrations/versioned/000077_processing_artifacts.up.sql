DO $$ BEGIN RAISE NOTICE '[Migration 000077] Creating processing artifacts...'; END $$;

CREATE TABLE IF NOT EXISTS processing_artifacts (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    stage VARCHAR(64) NOT NULL,
    key_version INTEGER NOT NULL,
    artifact_key CHAR(64) NOT NULL,
    processor_digest CHAR(64) NOT NULL,
    output_digest CHAR(64) NOT NULL,
    output_schema VARCHAR(64) NOT NULL,
    codec VARCHAR(32) NOT NULL,
    inline_payload BOOLEAN NOT NULL DEFAULT TRUE,
    payload BYTEA,
    object_ref TEXT NOT NULL DEFAULT '',
    payload_checksum CHAR(64) NOT NULL,
    size_bytes BIGINT NOT NULL,
    hit_count BIGINT NOT NULL DEFAULT 0,
    last_hit_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_processing_artifacts_key
        UNIQUE (tenant_id, stage, key_version, artifact_key),
    CONSTRAINT ck_processing_artifacts_payload
        CHECK (
            (inline_payload AND payload IS NOT NULL AND object_ref = '')
            OR
            (NOT inline_payload AND payload IS NULL AND object_ref <> '')
        ),
    CONSTRAINT ck_processing_artifacts_size CHECK (size_bytes >= 0),
    CONSTRAINT ck_processing_artifacts_hit_count CHECK (hit_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_processing_artifacts_tenant_created
    ON processing_artifacts (tenant_id, created_at);

CREATE TABLE IF NOT EXISTS knowledge_attempt_counters (
    knowledge_id VARCHAR(64) PRIMARY KEY,
    last_attempt INTEGER NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_knowledge_attempt_counters_last
        CHECK (last_attempt >= 0)
);

DO $$ BEGIN RAISE NOTICE '[Migration 000075] Processing artifacts ready'; END $$;
