DELETE FROM evaluation_model_calls WHERE task_id IS NULL;
ALTER TABLE evaluation_model_calls ALTER COLUMN task_id SET NOT NULL;
