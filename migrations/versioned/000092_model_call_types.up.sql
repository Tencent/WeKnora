ALTER TABLE evaluation_model_calls
    ADD COLUMN IF NOT EXISTS model_type VARCHAR(32) NOT NULL DEFAULT 'KnowledgeQA';
