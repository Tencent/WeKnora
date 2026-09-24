-- Migration 000110 down: drop the one-shot resync marker.

UPDATE data_sources
SET config = config #-'{settings,resync_required}'
WHERE deleted_at IS NULL
  AND type IN ('feishu', 'lark', 'feishu_drive', 'lark_drive')
  AND (config -> 'settings' -> 'resync_required') IS NOT NULL;
