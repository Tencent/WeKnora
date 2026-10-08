-- Restore messages.artifacts from the table before dropping it, so files
-- generated after the upgrade survive a rollback.
UPDATE messages m
SET artifacts = agg.artifacts
FROM (
    SELECT
        message_id,
        jsonb_agg(
            jsonb_build_object(
                'url', url,
                'file_name', file_name,
                'file_type', file_type,
                'file_size', file_size,
                'content_hash', content_hash,
                'source_path', source_path,
                'mod_time', NULLIF(mod_time, ''),
                'created_at', created_at
            )
            ORDER BY position
        ) AS artifacts
    FROM message_artifacts
    GROUP BY message_id
) agg
WHERE m.id = agg.message_id;

DROP INDEX IF EXISTS idx_message_artifacts_session_created;
DROP INDEX IF EXISTS idx_message_artifacts_message_position;
DROP TABLE IF EXISTS message_artifacts;
