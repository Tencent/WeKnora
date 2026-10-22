-- SQLite counterpart of PostgreSQL migration 000134.
-- Persist the PDF source page of each gallery image so page order does not
-- depend on concurrent multimodal task completion time.

ALTER TABLE chunk_images ADD COLUMN source_page INTEGER NOT NULL DEFAULT 0;

UPDATE chunk_images
SET source_page = COALESCE((
    SELECT MIN(CAST(json_extract(locator.value, '$.page') AS INTEGER))
    FROM chunks c, json_each(
        CASE WHEN json_valid(c.source_locators) AND json_type(c.source_locators) = 'array'
             THEN c.source_locators ELSE '[]' END
    ) AS locator
    WHERE c.id = chunk_images.chunk_id
      AND json_extract(locator.value, '$.type') = 'pdf'
      AND CAST(json_extract(locator.value, '$.page') AS INTEGER) > 0
), 0);

CREATE INDEX IF NOT EXISTS idx_chunk_images_source_page_listing
    ON chunk_images(knowledge_base_id, tenant_id, source_page, chunk_id, image_index);

-- The existing triggers project image_info. This companion trigger runs after
-- each projected row is inserted and keeps the base trigger contract intact.
DROP TRIGGER IF EXISTS trg_chunk_images_source_page;
CREATE TRIGGER trg_chunk_images_source_page AFTER INSERT ON chunk_images
BEGIN
    UPDATE chunk_images
    SET source_page = COALESCE(
        CASE WHEN CAST(json_extract(image_info.value, '$.source_page') AS INTEGER) > 0
             THEN CAST(json_extract(image_info.value, '$.source_page') AS INTEGER) END,
        (
            SELECT MIN(CAST(json_extract(locator.value, '$.page') AS INTEGER))
            FROM chunks c, json_each(
                CASE WHEN json_valid(c.source_locators) AND json_type(c.source_locators) = 'array'
                     THEN c.source_locators ELSE '[]' END
            ) AS locator
            WHERE c.id = NEW.chunk_id
              AND json_extract(locator.value, '$.type') = 'pdf'
              AND CAST(json_extract(locator.value, '$.page') AS INTEGER) > 0
        ),
        0
    )
    FROM json_each((SELECT image_info FROM chunks WHERE id = NEW.chunk_id)) AS image_info
    WHERE chunk_images.chunk_id = NEW.chunk_id
      AND chunk_images.image_index = NEW.image_index
      AND CAST(image_info.key AS INTEGER) = NEW.image_index;
END;

-- source_locators changes do not rebuild image_info, so refresh the projected
-- page values directly when an edit invalidates or replaces source positions.
DROP TRIGGER IF EXISTS trg_chunk_images_source_page_locator_update;
CREATE TRIGGER trg_chunk_images_source_page_locator_update
AFTER UPDATE OF source_locators ON chunks
WHEN OLD.source_locators IS NOT NEW.source_locators
BEGIN
    UPDATE chunk_images
    SET source_page = COALESCE(
        CASE WHEN CAST(json_extract(image_info.value, '$.source_page') AS INTEGER) > 0
             THEN CAST(json_extract(image_info.value, '$.source_page') AS INTEGER) END,
        (
            SELECT MIN(CAST(json_extract(locator.value, '$.page') AS INTEGER))
            FROM json_each(
                CASE WHEN json_valid(NEW.source_locators) AND json_type(NEW.source_locators) = 'array'
                     THEN NEW.source_locators ELSE '[]' END
            ) AS locator
            WHERE json_extract(locator.value, '$.type') = 'pdf'
              AND CAST(json_extract(locator.value, '$.page') AS INTEGER) > 0
        ),
        0
    )
    FROM json_each(NEW.image_info) AS image_info
    WHERE chunk_images.chunk_id = NEW.id
      AND chunk_images.image_index = CAST(image_info.key AS INTEGER);
END;
