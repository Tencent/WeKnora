DROP INDEX IF EXISTS idx_sessions_tenant_kind_user_updated;
ALTER TABLE sessions DROP COLUMN kind;
