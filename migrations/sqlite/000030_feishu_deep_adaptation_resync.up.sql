-- Migration 000030: feishu deep adaptation — one-shot full-resync marker.
-- (Postgres twin: versioned/000110_feishu_deep_adaptation_resync.)
--
-- Legacy feishu/lark data sources created before the deep-adaptation feature
-- hold documents ingested through the export path with no knowledge-base
-- folder paths. This stamps their Settings with a one-shot resync_required
-- marker; the sync entry upgrades the next run (even a scheduled incremental
-- one) to a full pass so documents re-ingest with folder paths and
-- block-level parsing, then clears the marker.

UPDATE data_sources
SET config = json_set(config, '$.settings.resync_required', json('true'))
WHERE deleted_at IS NULL
  AND type IN ('feishu', 'lark', 'feishu_drive', 'lark_drive')
  AND json_extract(config, '$.settings.resync_required') IS NULL;
