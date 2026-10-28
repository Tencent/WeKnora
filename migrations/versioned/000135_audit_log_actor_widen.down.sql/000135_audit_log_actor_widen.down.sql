-- Revert 000135: narrow the audit_logs user id columns back to VARCHAR(36).
ALTER TABLE audit_logs ALTER COLUMN actor_user_id TYPE VARCHAR(36);
ALTER TABLE audit_logs ALTER COLUMN target_user_id TYPE VARCHAR(36);
