DROP INDEX IF EXISTS idx_chunks_quality_status;
ALTER TABLE chunks DROP COLUMN IF EXISTS quality_status;
ALTER TABLE chunks DROP COLUMN IF EXISTS recall_weight;
ALTER TABLE chunks DROP COLUMN IF EXISTS feedback_positive_rate;
