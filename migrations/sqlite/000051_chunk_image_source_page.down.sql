DROP TRIGGER IF EXISTS trg_chunk_images_source_page_locator_update;
DROP TRIGGER IF EXISTS trg_chunk_images_source_page;
DROP INDEX IF EXISTS idx_chunk_images_source_page_listing;
ALTER TABLE chunk_images DROP COLUMN source_page;
