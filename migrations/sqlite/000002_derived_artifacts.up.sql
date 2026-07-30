CREATE TABLE IF NOT EXISTS derived_artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    artifact_key TEXT NOT NULL,
    artifact_kind TEXT NOT NULL,
    input_digest TEXT NOT NULL,
    model_id TEXT NOT NULL DEFAULT '', model_revision TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '', config_digest TEXT NOT NULL DEFAULT '',
    producer_version TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
    payload BLOB, payload_encoding TEXT NOT NULL DEFAULT '', object_uri TEXT,
    payload_digest TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '', attempt_count INTEGER NOT NULL DEFAULT 0,
    owner_token TEXT NOT NULL DEFAULT '', lease_expires_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, completed_at DATETIME,
    UNIQUE (tenant_id, artifact_key),
    CHECK (length(artifact_key) = 64),
    CHECK (length(owner_token) <= 128),
    CHECK (length(error_message) <= 2048),
    CHECK (status IN ('pending','computing','succeeded','failed'))
);
CREATE INDEX idx_derived_artifacts_kind_status ON derived_artifacts(artifact_kind, status);
CREATE INDEX idx_derived_artifacts_lease ON derived_artifacts(status, lease_expires_at);
