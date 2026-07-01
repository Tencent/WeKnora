-- SQLite stores VARCHAR lengths as affinity only. Keep this migration as a
-- no-op so versioned migration numbers stay aligned with Postgres.
SELECT 1;
