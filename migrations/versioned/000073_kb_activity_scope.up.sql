-- Add a generic parent scope to audit rows so one immutable audit stream can
-- project resource-specific activity feeds without one log table per feature.
ALTER TABLE audit_logs
    ADD COLUMN IF NOT EXISTS scope_type VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS scope_id VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant_scope_desc
    ON audit_logs (tenant_id, scope_type, scope_id, id DESC);
