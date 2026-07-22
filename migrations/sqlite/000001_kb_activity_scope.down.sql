DROP INDEX IF EXISTS idx_audit_logs_tenant_scope_desc;

ALTER TABLE audit_logs DROP COLUMN scope_id;
ALTER TABLE audit_logs DROP COLUMN scope_type;
