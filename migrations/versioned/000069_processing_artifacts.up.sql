DO $$ BEGIN RAISE NOTICE '[Migration 000066] Creating processing_artifacts...'; END $$;

CREATE TABLE IF NOT EXISTS processing_artifacts (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    stage VARCHAR(64) NOT NULL,
    key_version INTEGER NOT NULL,
    input_fingerprint CHAR(64) NOT NULL,
    payload BYTEA NULL,
    object_path TEXT NOT NULL DEFAULT '',
    content_sha256 CHAR(64) NOT NULL,
    size_bytes BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_processing_artifacts_key
        UNIQUE (tenant_id, stage, key_version, input_fingerprint)
);

CREATE INDEX IF NOT EXISTS idx_processing_artifacts_tenant_created
    ON processing_artifacts (tenant_id, created_at);

CREATE INDEX IF NOT EXISTS idx_processing_artifacts_created_id
    ON processing_artifacts (created_at, id);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] processing_artifacts ready'; END $$;
