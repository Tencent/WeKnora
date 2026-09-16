-- Ledger for one-shot Go data repairs. The knowledge-image binding backfill
-- itself lives in application code so Postgres and SQLite share
-- ScanResourceReferences rather than diverging regexp SQL.

CREATE TABLE IF NOT EXISTS data_patches (
    id VARCHAR(64) PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
