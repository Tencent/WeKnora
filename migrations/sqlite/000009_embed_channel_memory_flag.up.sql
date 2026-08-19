-- Mirrors versioned migration 000060_embed_channels: embed_channels.allow_memory.

ALTER TABLE embed_channels ADD COLUMN allow_memory BOOLEAN NOT NULL DEFAULT 0;
