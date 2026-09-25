-- Migration 000114: positions of each chunk in its original file, used to
-- open a cited document at the cited page, slide, rows or time.
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS source_locators JSONB;
