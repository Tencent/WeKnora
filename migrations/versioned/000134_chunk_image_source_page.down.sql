DROP TRIGGER IF EXISTS trg_chunk_images_source_page_locator_update ON chunks;
DROP TRIGGER IF EXISTS trg_chunk_images_source_page ON chunk_images;
DROP FUNCTION IF EXISTS chunk_images_source_page_refresh_chunk();
DROP FUNCTION IF EXISTS chunk_images_source_page_sync();
DROP INDEX IF EXISTS idx_chunk_images_source_page_listing;
ALTER TABLE chunk_images DROP COLUMN IF EXISTS source_page;