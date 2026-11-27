-- Mirrors versioned migration 000138_agent_prompt_principal_width.
-- SQLite TEXT columns do not enforce lengths, so agent_prompts already accepts
-- prefixed embed-session principals. This migration keeps the SQLite schema
-- version aligned with the PostgreSQL migration that widens user_id and
-- resolved_by for PostgreSQL.
SELECT 1;