DROP INDEX IF EXISTS idx_question_events_cluster_id;
DROP INDEX IF EXISTS idx_question_events_video_id;
DROP INDEX IF EXISTS idx_question_events_asked_at;
DROP INDEX IF EXISTS idx_question_events_event_id;
DROP TABLE IF EXISTS dashboard_question_events;

DROP INDEX IF EXISTS idx_question_stats_stat_date_unique;
DROP INDEX IF EXISTS idx_question_clusters_question_key;
ALTER TABLE dashboard_question_clusters
    DROP COLUMN IF EXISTS question_key;
