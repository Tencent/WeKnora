ALTER TABLE evaluation_tasks ADD COLUMN runtime_metrics TEXT;
ALTER TABLE evaluation_question_results ADD COLUMN usage_reported INTEGER NOT NULL DEFAULT 0;
