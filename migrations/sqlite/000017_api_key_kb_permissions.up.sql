-- Mirrors versioned migration 000096_api_key_kb_permissions:
-- per-KB retrieve/ingest/manage_kbs overlay on scoped tenant API keys.

ALTER TABLE tenant_api_keys ADD COLUMN knowledge_base_permissions TEXT NOT NULL DEFAULT '{}';
