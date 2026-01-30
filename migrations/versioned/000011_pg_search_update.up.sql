-- Migration 000011: Update pg_search extension to latest version
-- Equivalent to: psql -c 'ALTER EXTENSION pg_search UPDATE;'

ALTER EXTENSION pg_search UPDATE;
