DROP TRIGGER IF EXISTS trg_chunk_images_insert ON chunks;
DROP TRIGGER IF EXISTS trg_chunk_images_update ON chunks;
DROP TRIGGER IF EXISTS trg_chunk_images_delete ON chunks;
DROP FUNCTION IF EXISTS chunk_images_sync();
DROP FUNCTION IF EXISTS chunk_images_try_jsonb(TEXT);
DROP TABLE IF EXISTS chunk_images;
