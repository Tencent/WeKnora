ALTER TABLE sessions ADD COLUMN IF NOT EXISTS parent_session_id VARCHAR(36);
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS forked_from_message_id VARCHAR(36);
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS fork_bootstrap JSONB;

CREATE INDEX IF NOT EXISTS idx_sessions_parent_session_id
    ON sessions (parent_session_id)
    WHERE parent_session_id IS NOT NULL;

-- Drives the orphan-snapshot reaper, which scans for forks whose bootstrap was
-- never consumed. Partial so the index stays tiny: the overwhelming majority of
-- sessions have no bootstrap at all.
CREATE INDEX IF NOT EXISTS idx_sessions_unconsumed_fork
    ON sessions ((fork_bootstrap ->> 'snapshot_id'))
    WHERE fork_bootstrap IS NOT NULL
      AND fork_bootstrap ->> 'consumed_at' IS NULL;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS sandbox_checkpoint JSONB;
