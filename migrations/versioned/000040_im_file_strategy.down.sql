-- Migration: 000040_im_file_strategy (rollback)
DO $$ BEGIN RAISE NOTICE '[Migration 000040] Removing file processing strategy from IM channels'; END $$;

ALTER TABLE im_channels
    DROP CONSTRAINT IF EXISTS chk_im_channels_file_strategy;

ALTER TABLE im_channels
    DROP COLUMN IF EXISTS file_processing_strategy;

DO $$ BEGIN RAISE NOTICE '[Migration 000040] File processing strategy removed'; END $$;
