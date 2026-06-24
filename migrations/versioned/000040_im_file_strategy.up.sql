-- Migration: 000040_im_file_strategy
-- Description: Add file_processing_strategy column to im_channels to support
--              both "kb" (upload to knowledge base) and "direct" (inject into
--              conversation) file handling modes.
DO $$ BEGIN RAISE NOTICE '[Migration 000040] Adding file processing strategy to IM channels'; END $$;

ALTER TABLE im_channels
    ADD COLUMN IF NOT EXISTS file_processing_strategy VARCHAR(20) NOT NULL DEFAULT 'kb';

DO $$ BEGIN
    ALTER TABLE im_channels
        ADD CONSTRAINT chk_im_channels_file_strategy
        CHECK (file_processing_strategy IN ('kb', 'direct'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

COMMENT ON COLUMN im_channels.file_processing_strategy IS
    'File handling strategy: kb (upload to knowledge base, default) or direct (parse and inject into conversation)';

DO $$ BEGIN RAISE NOTICE '[Migration 000040] File processing strategy added'; END $$;
