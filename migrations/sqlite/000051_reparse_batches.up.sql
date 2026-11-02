CREATE TABLE IF NOT EXISTS reparse_batches (
 id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, knowledge_base_id TEXT NOT NULL,
 mode TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', manifest_sha256 TEXT NOT NULL,
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_reparse_batches_tenant_kb_created ON reparse_batches (tenant_id, knowledge_base_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_reparse_batches_tracking_manifest ON reparse_batches (tenant_id, knowledge_base_id, manifest_sha256) WHERE mode = 'tracking_only';
CREATE TABLE IF NOT EXISTS reparse_batch_members (
 batch_id TEXT NOT NULL, knowledge_id TEXT NOT NULL, submission_id TEXT NOT NULL DEFAULT '', submission_state TEXT NOT NULL DEFAULT 'waiting_start', submission_error TEXT NOT NULL DEFAULT '',
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (batch_id, knowledge_id)
);
CREATE INDEX IF NOT EXISTS idx_reparse_batch_members_batch_state ON reparse_batch_members (batch_id, submission_state);
CREATE INDEX IF NOT EXISTS idx_reparse_batch_members_submission ON reparse_batch_members (submission_id);
CREATE TABLE IF NOT EXISTS reparse_batch_submissions (
 id TEXT PRIMARY KEY, batch_id TEXT NOT NULL, tenant_id INTEGER NOT NULL, idempotency_key TEXT NOT NULL DEFAULT '', request_sha256 TEXT NOT NULL,
 state TEXT NOT NULL, task_id TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_reparse_submission_idempotency ON reparse_batch_submissions (tenant_id, idempotency_key) WHERE idempotency_key <> '';
