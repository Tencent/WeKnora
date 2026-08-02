-- SQLite rollback for migration 000002 processing artifacts and attempt fencing.
DROP TABLE IF EXISTS knowledge_attempt_counters;
DROP TABLE IF EXISTS processing_artifacts;
