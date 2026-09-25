DROP INDEX IF EXISTS idx_audit_logs_tenant_scope_desc;

ALTER TABLE audit_logs
    DROP COLUMN IF EXISTS scope_id,
    DROP COLUMN IF EXISTS scope_type;
