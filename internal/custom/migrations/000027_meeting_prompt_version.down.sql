ALTER TABLE meeting_orchestration_jobs
  ALTER COLUMN prompt_version TYPE VARCHAR(64)
  USING LEFT(prompt_version, 64);
