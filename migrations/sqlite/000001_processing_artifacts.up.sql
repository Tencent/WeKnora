CREATE TABLE IF NOT EXISTS processing_artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    stage VARCHAR(64) NOT NULL,
    key_version INTEGER NOT NULL,
    artifact_key CHAR(64) NOT NULL,
    processor_digest CHAR(64) NOT NULL,
    output_digest CHAR(64) NOT NULL,
    output_schema VARCHAR(64) NOT NULL,
    codec VARCHAR(32) NOT NULL,
    inline_payload BOOLEAN NOT NULL DEFAULT 1,
    payload BLOB,
    object_ref TEXT NOT NULL DEFAULT '',
    payload_checksum CHAR(64) NOT NULL,
    size_bytes INTEGER NOT NULL,
    hit_count INTEGER NOT NULL DEFAULT 0,
    last_hit_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_processing_artifacts_key
        UNIQUE (tenant_id, stage, key_version, artifact_key),
    CONSTRAINT ck_processing_artifacts_payload
        CHECK (
            (inline_payload = 1 AND payload IS NOT NULL AND object_ref = '')
            OR
            (inline_payload = 0 AND payload IS NULL AND object_ref <> '')
        ),
    CONSTRAINT ck_processing_artifacts_size CHECK (size_bytes >= 0),
    CONSTRAINT ck_processing_artifacts_hit_count CHECK (hit_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_processing_artifacts_tenant_created
    ON processing_artifacts (tenant_id, created_at);

CREATE TABLE IF NOT EXISTS knowledge_attempt_counters (
    knowledge_id VARCHAR(64) PRIMARY KEY,
    last_attempt INTEGER NOT NULL CHECK (last_attempt >= 0),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
