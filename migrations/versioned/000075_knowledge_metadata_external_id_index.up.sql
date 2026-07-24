DO $$ BEGIN RAISE NOTICE '[Migration 000075] Adding knowledge metadata external_id prefix index...'; END $$;

-- Speeds up the datasource subtree sweep (repository FindByMetadataKeyPrefix),
-- which runs on every re-sync of a multi-item source node:
--   WHERE knowledge_base_id = $1 AND deleted_at IS NULL
--     AND metadata->>'external_id' LIKE '<parent>#%' ESCAPE '\'
--
-- The knowledge_base_id equality is already served by idx_knowledges_base_id,
-- but the metadata->>'external_id' LIKE-prefix is only a post-filter (Bitmap
-- Heap Scan recheck). This composite expression index lets the prefix match be
-- served by the index too. Two deliberate choices:
--   * text_pattern_ops — a plain btree opclass is NOT used for LIKE 'x%' in a
--     non-C collation; text_pattern_ops compares raw bytes, so it drives the
--     prefix match regardless of the database collation (this DB even carries a
--     collation-version mismatch, which text_pattern_ops sidesteps entirely).
--   * partial on deleted_at IS NULL — matches the query's own predicate and
--     keeps the index limited to live rows.
-- Non-destructive: index-only, no schema or data change. IF NOT EXISTS makes it
-- idempotent and safe to re-run.
CREATE INDEX IF NOT EXISTS idx_knowledges_kb_metadata_external_id
    ON knowledges (knowledge_base_id, (metadata->>'external_id') text_pattern_ops)
    WHERE deleted_at IS NULL;
