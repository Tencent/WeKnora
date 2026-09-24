-- Migration: 000114_intent_policies_lineage_version_unique
-- 谱系版本唯一性（并发防护）：同一 (tenant, scope_type, scope_ref) 谱系内
-- version 唯一。CREATE 的 read-then-insert 与 PUT 的 nextVersion 计算在
-- 并发下都会双插入成功；数据库级唯一索引是跨 postgres/sqlite 都成立的
-- 兜底——冲突映射为 409，调用方重试/改用 PUT。
DO $$ BEGIN RAISE NOTICE '[Migration 000114] Adding unique index on intent_policies (tenant, scope, version)'; END $$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_intent_policies_lineage_version
    ON intent_policies (tenant_id, scope_type, scope_ref, version);
