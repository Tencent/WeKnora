-- NULL preserves the legacy capabilities + KB allow-list model; {} denies all KBs.
ALTER TABLE tenant_api_keys ADD COLUMN IF NOT EXISTS knowledge_base_permissions JSONB;
