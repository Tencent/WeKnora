ALTER TABLE dashboard_question_clusters
    ADD COLUMN IF NOT EXISTS question_key TEXT;

CREATE INDEX IF NOT EXISTS idx_question_clusters_question_key
    ON dashboard_question_clusters(question_key);

-- dashboard_question_stats is a derived table. Older releases did not enforce
-- one row per date, so retain the newest aggregate before adding the constraint.
DELETE FROM dashboard_question_stats
WHERE id IN (
    SELECT id
    FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY stat_date
                   ORDER BY updated_at DESC NULLS LAST,
                            created_at DESC NULLS LAST,
                            id DESC
               ) AS duplicate_rank
        FROM dashboard_question_stats
    ) ranked
    WHERE ranked.duplicate_rank > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_question_stats_stat_date_unique
    ON dashboard_question_stats(stat_date);

CREATE TABLE IF NOT EXISTS dashboard_question_events (
    id VARCHAR(36) PRIMARY KEY,
    event_id VARCHAR(128) NOT NULL,
    session_id VARCHAR(64),
    video_id VARCHAR(36),
    video_title VARCHAR(255),
    video_category VARCHAR(50),
    video_seconds INTEGER NOT NULL DEFAULT 0,
    cluster_id VARCHAR(36),
    question TEXT NOT NULL,
    asked_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_question_events_event_id
    ON dashboard_question_events(event_id);

CREATE INDEX IF NOT EXISTS idx_question_events_asked_at
    ON dashboard_question_events(asked_at);

CREATE INDEX IF NOT EXISTS idx_question_events_video_id
    ON dashboard_question_events(video_id);

CREATE INDEX IF NOT EXISTS idx_question_events_cluster_id
    ON dashboard_question_events(cluster_id);
