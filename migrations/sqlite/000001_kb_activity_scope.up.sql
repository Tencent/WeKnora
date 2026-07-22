ALTER TABLE audit_logs ADD COLUMN scope_type VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE audit_logs ADD COLUMN scope_id VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant_scope_desc
    ON audit_logs(tenant_id, scope_type, scope_id, id DESC);
