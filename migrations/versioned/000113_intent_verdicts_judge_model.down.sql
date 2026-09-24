-- Migration: 000113_intent_verdicts_judge_model (rollback)
DO $$ BEGIN RAISE NOTICE '[Migration 000113] Dropping intent_verdicts.judge_model'; END $$;

ALTER TABLE intent_verdicts DROP COLUMN IF EXISTS judge_model;
