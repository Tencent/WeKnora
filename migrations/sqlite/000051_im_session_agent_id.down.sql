-- Rollback: 000051_im_session_agent_id
-- Restores the indexes without agent_id. Rows created per agent after an
-- upgrade would collide once the narrower index is back, so collapse them
-- first: keep the most recently updated row for each old key, soft-delete the
-- rest. Without this the rollback would fail on a database that has switched
-- agents.

UPDATE im_channel_sessions
   SET deleted_at = CURRENT_TIMESTAMP,
       updated_at = CURRENT_TIMESTAMP
 WHERE deleted_at IS NULL
   AND thread_id = ''
   AND id NOT IN (
       SELECT id
         FROM (
              SELECT id,
                     ROW_NUMBER() OVER (
                         PARTITION BY platform, user_id, chat_id, tenant_id
                         ORDER BY updated_at DESC, id DESC
                     ) AS rn
                FROM im_channel_sessions
               WHERE deleted_at IS NULL
                 AND thread_id = ''
         ) ranked
        WHERE rn = 1
   );

UPDATE im_channel_sessions
   SET deleted_at = CURRENT_TIMESTAMP,
       updated_at = CURRENT_TIMESTAMP
 WHERE deleted_at IS NULL
   AND thread_id != ''
   AND id NOT IN (
       SELECT id
         FROM (
              SELECT id,
                     ROW_NUMBER() OVER (
                         PARTITION BY platform, chat_id, thread_id, tenant_id
                         ORDER BY updated_at DESC, id DESC
                     ) AS rn
                FROM im_channel_sessions
               WHERE deleted_at IS NULL
                 AND thread_id != ''
         ) ranked
        WHERE rn = 1
   );

DROP INDEX IF EXISTS idx_channel_lookup;
CREATE UNIQUE INDEX idx_channel_lookup
    ON im_channel_sessions (platform, user_id, chat_id, tenant_id)
    WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_channel_thread_lookup;
CREATE UNIQUE INDEX idx_channel_thread_lookup
    ON im_channel_sessions (platform, chat_id, thread_id, tenant_id)
    WHERE deleted_at IS NULL AND thread_id != '';
