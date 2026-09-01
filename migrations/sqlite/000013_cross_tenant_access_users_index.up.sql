-- SQLite equivalent of PostgreSQL migration 000089. The partial index serves
-- both COUNT and newest-first list queries for active cross-tenant users.
CREATE INDEX IF NOT EXISTS idx_users_cross_tenant_access_list
    ON users (created_at DESC, id ASC)
    WHERE deleted_at IS NULL AND can_access_all_tenants = 1;
