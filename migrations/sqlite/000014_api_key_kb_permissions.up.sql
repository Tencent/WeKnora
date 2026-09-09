-- NULL is legacy uniform authorization; {} is explicit deny-all.
ALTER TABLE tenant_api_keys ADD COLUMN knowledge_base_permissions TEXT;
