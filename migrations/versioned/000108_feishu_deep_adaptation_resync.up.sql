-- Migration 000108: feishu deep adaptation — one-shot full-resync marker.
--
-- Legacy feishu/lark data sources created before the deep-adaptation feature
-- hold documents ingested through the export path with no knowledge-base
-- folder paths. This stamps their Settings with a one-shot resync_required
-- marker; the sync entry upgrades the next run (even a scheduled incremental
-- one) to a full pass so documents re-ingest with folder paths and
-- block-level parsing, then clears the marker.
--
-- Post-upgrade data sources already carry settings.parse_mode (written by the
-- edit form) and are skipped by the IS NULL guard.

UPDATE data_sources
SET config = jsonb_set(config, '{settings,resync_required}', 'true'::jsonb, true)
WHERE deleted_at IS NULL
  AND type IN ('feishu', 'lark', 'feishu_drive', 'lark_drive')
  AND (config -> 'settings' -> 'resync_required') IS NULL;
