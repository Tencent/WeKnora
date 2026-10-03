-- Restore messages.artifacts from the table before dropping it, so files
-- generated after the upgrade survive a rollback. created_at is normalised to
-- RFC 3339 so Go can decode the restored JSON; mod_time is already RFC 3339.
UPDATE messages
SET artifacts = (
    SELECT json_group_array(json(item))
    FROM (
        SELECT json_object(
            'url', url,
            'file_name', file_name,
            'file_type', file_type,
            'file_size', file_size,
            'content_hash', content_hash,
            'source_path', source_path,
            'mod_time', NULLIF(mod_time, ''),
            'created_at', strftime('%Y-%m-%dT%H:%M:%fZ', created_at)
        ) AS item
        FROM message_artifacts
        WHERE message_artifacts.message_id = messages.id
        ORDER BY position
    )
)
WHERE id IN (SELECT message_id FROM message_artifacts);

DROP INDEX IF EXISTS idx_message_artifacts_session_created;
DROP INDEX IF EXISTS idx_message_artifacts_message_position;
DROP TABLE IF EXISTS message_artifacts;
