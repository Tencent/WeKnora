-- Migration 000030 down: drop the one-shot resync marker.

UPDATE data_sources
SET config = json_remove(config, '$.settings.resync_required')
WHERE deleted_at IS NULL
  AND type IN ('feishu', 'lark', 'feishu_drive', 'lark_drive')
  AND json_extract(config, '$.settings.resync_required') IS NOT NULL;
