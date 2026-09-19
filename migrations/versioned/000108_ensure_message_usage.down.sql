-- Intentionally a no-op.
--
-- messages.usage is owned by 000085_message_usage, whose own down migration
-- drops the column. Dropping it here as well would delete the column on healthy
-- databases (where 000085 really did run) as soon as someone rolled back this
-- guard, turning a bookkeeping rollback into silent data loss.
SELECT 1;
