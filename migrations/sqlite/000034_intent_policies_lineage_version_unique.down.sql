-- Mirrors versioned migration 000114_intent_policies_lineage_version_unique (rollback)。

DROP INDEX IF EXISTS idx_intent_policies_lineage_version;
