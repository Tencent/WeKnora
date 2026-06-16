ALTER TABLE tenant_members ADD COLUMN storage_quota BIGINT DEFAULT 0;
ALTER TABLE tenant_members ADD COLUMN storage_used  BIGINT DEFAULT 0;
