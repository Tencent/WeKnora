-- Migration 000122: feishu deep adaptation — one-shot full-resync marker.
--
-- Legacy feishu/lark data sources created before the deep-adaptation feature
-- hold documents ingested through the export path with no knowledge-base
-- folder paths. This stamps their Settings with a one-shot resync_required
-- marker; the sync entry upgrades the next run (even a scheduled incremental
-- one) to a full pass so documents re-ingest with folder paths and
-- block-level parsing, then clears the marker.
--
-- Settings is optional; API-created sources may store it as JSON null.
-- Normalize the parent object before writing its nested marker.

UPDATE data_sources
SET config = jsonb_set(config, '{settings}',
    (CASE WHEN jsonb_typeof(config -> 'settings') = 'object'
          THEN config -> 'settings' ELSE '{}'::jsonb END)
    || '{"resync_required":true}'::jsonb, true)
WHERE deleted_at IS NULL
  AND type IN ('feishu', 'lark', 'feishu_drive', 'lark_drive')
  AND (config -> 'settings' -> 'resync_required') IS NULL;
