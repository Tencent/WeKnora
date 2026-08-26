-- Keep cross-tenant access management paging and counts proportional to the
-- small privileged-user subset instead of scanning and sorting all users.
CREATE INDEX IF NOT EXISTS idx_users_cross_tenant_access_list
    ON users (created_at DESC, id ASC)
    WHERE deleted_at IS NULL AND can_access_all_tenants = TRUE;
