DROP INDEX idx_audit_logs_tenant_scope_desc ON audit_logs;

ALTER TABLE audit_logs
    DROP COLUMN scope_id,
    DROP COLUMN scope_type;
