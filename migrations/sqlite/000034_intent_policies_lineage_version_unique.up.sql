-- Mirrors versioned migration 000114_intent_policies_lineage_version_unique：
-- 谱系版本唯一性（并发防护），同 (tenant, scope_type, scope_ref) 内 version
-- 唯一；冲突由 handler 映射为 409。

CREATE UNIQUE INDEX IF NOT EXISTS idx_intent_policies_lineage_version
    ON intent_policies (tenant_id, scope_type, scope_ref, version);
