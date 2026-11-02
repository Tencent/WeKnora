CREATE TABLE IF NOT EXISTS reparse_batches (
 id VARCHAR(36) PRIMARY KEY, tenant_id BIGINT NOT NULL, knowledge_base_id VARCHAR(36) NOT NULL,
 mode VARCHAR(24) NOT NULL, name TEXT NOT NULL DEFAULT '', manifest_sha256 VARCHAR(64) NOT NULL,
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_reparse_batches_tenant_kb_created ON reparse_batches (tenant_id, knowledge_base_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_reparse_batches_tracking_manifest ON reparse_batches (tenant_id, knowledge_base_id, manifest_sha256) WHERE mode = 'tracking_only';
CREATE TABLE IF NOT EXISTS reparse_batch_members (
 batch_id VARCHAR(36) NOT NULL, knowledge_id VARCHAR(36) NOT NULL, submission_id VARCHAR(36) NOT NULL DEFAULT '', submission_state VARCHAR(24) NOT NULL DEFAULT 'waiting_start', submission_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (batch_id, knowledge_id)
);
CREATE INDEX IF NOT EXISTS idx_reparse_batch_members_batch_state ON reparse_batch_members (batch_id, submission_state);
CREATE INDEX IF NOT EXISTS idx_reparse_batch_members_submission ON reparse_batch_members (submission_id);
CREATE TABLE IF NOT EXISTS reparse_batch_submissions (
 id VARCHAR(36) PRIMARY KEY, batch_id VARCHAR(36) NOT NULL, tenant_id BIGINT NOT NULL, idempotency_key VARCHAR(255) NOT NULL DEFAULT '', request_sha256 VARCHAR(64) NOT NULL,
 state VARCHAR(24) NOT NULL, task_id VARCHAR(128) NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_reparse_submission_idempotency ON reparse_batch_submissions (tenant_id, idempotency_key) WHERE idempotency_key <> '';
