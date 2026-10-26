-- Data cleanup is intentionally not reversed: we cannot distinguish rows
-- removed by this migration from rows subsequently deleted by a user.
SELECT 1;
