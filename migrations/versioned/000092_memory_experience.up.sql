-- Unified user-memory and execution-experience extraction.
ALTER TABLE memory_items ADD COLUMN experience JSONB;
ALTER TABLE memory_subjects ADD COLUMN extraction_progress JSONB;
ALTER TABLE memory_subjects ADD COLUMN extract_lease_token VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE memory_subjects ADD COLUMN extract_lease_until TIMESTAMPTZ;
