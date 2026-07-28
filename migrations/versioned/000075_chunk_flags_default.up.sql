-- Reassert the default required by chunk creation paths.
ALTER TABLE chunks
    ALTER COLUMN flags SET DEFAULT 1;
