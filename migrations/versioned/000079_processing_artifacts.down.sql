-- Roll back migration 000079 processing-artifact persistence and attempt fencing.
DROP TABLE IF EXISTS knowledge_attempt_counters;
DROP TABLE IF EXISTS processing_artifacts;
