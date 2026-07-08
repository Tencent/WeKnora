ALTER TABLE chunks ADD COLUMN IF NOT EXISTS feedback_like_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS feedback_dislike_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS feedback_positive_rate DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS recall_weight DOUBLE PRECISION NOT NULL DEFAULT 1;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS quality_status VARCHAR(32) NOT NULL DEFAULT 'normal';

UPDATE chunks
SET feedback_positive_rate = CASE
        WHEN COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0) > 0
        THEN CAST(COALESCE(feedback_like_count, 0) AS DOUBLE PRECISION)
             / (COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0))
        ELSE 0
    END,
    recall_weight = CASE
        WHEN COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0) < 1 THEN 1
        WHEN CAST(COALESCE(feedback_like_count, 0) AS DOUBLE PRECISION)
             / NULLIF(COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0), 0) < 0.2 THEN 0.7
        WHEN CAST(COALESCE(feedback_like_count, 0) AS DOUBLE PRECISION)
             / NULLIF(COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0), 0) < 0.5 THEN 0.7
        WHEN CAST(COALESCE(feedback_like_count, 0) AS DOUBLE PRECISION)
             / NULLIF(COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0), 0) >= 0.8 THEN 1.2
        ELSE 1
    END,
    quality_status = CASE
        WHEN COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0) < 1 THEN 'normal'
        WHEN CAST(COALESCE(feedback_like_count, 0) AS DOUBLE PRECISION)
             / NULLIF(COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0), 0) < 0.2 THEN 'needs_optimization'
        WHEN CAST(COALESCE(feedback_like_count, 0) AS DOUBLE PRECISION)
             / NULLIF(COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0), 0) < 0.5 THEN 'deprioritized'
        WHEN CAST(COALESCE(feedback_like_count, 0) AS DOUBLE PRECISION)
             / NULLIF(COALESCE(feedback_like_count, 0) + COALESCE(feedback_dislike_count, 0), 0) >= 0.8 THEN 'boosted'
        ELSE 'normal'
    END;

CREATE INDEX IF NOT EXISTS idx_chunks_quality_status ON chunks(quality_status);
