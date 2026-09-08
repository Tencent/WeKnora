-- Unified user-memory and execution-experience extraction.
ALTER TABLE memory_items ADD COLUMN experience TEXT;
ALTER TABLE memory_subjects ADD COLUMN extraction_progress TEXT;
ALTER TABLE memory_subjects ADD COLUMN extract_lease_token VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE memory_subjects ADD COLUMN extract_lease_until DATETIME;
