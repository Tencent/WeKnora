-- Migration 000134: persist the stable PDF page of each gallery image.
--
-- Images are processed concurrently, so chunks.created_at reflects completion
-- order rather than the page that produced a PDF image. source_page is
-- backfilled from the owning chunk's source_locators and maintained from the
-- source_page embedded in new image_info entries.

ALTER TABLE chunk_images ADD COLUMN IF NOT EXISTS source_page INTEGER NOT NULL DEFAULT 0;

UPDATE chunk_images i
SET source_page = COALESCE(page_source.page, 0)
FROM chunks c
LEFT JOIN LATERAL (
    SELECT MIN((locator->>'page')::int) AS page
    FROM jsonb_array_elements(COALESCE(c.source_locators, '[]'::jsonb)) AS locator
    WHERE locator->>'type' = 'pdf'
      AND (locator->>'page') ~ '^[1-9][0-9]*$'
) AS page_source ON true
WHERE c.id = i.chunk_id;

CREATE INDEX IF NOT EXISTS idx_chunk_images_source_page_listing
    ON chunk_images(knowledge_base_id, tenant_id, source_page, chunk_id, image_index);

-- Existing chunk_images triggers own the JSON projection. This companion
-- trigger runs after each projected row is inserted, keeping the original
-- trigger contract intact while copying the source page into a sortable column.
CREATE OR REPLACE FUNCTION chunk_images_source_page_sync() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE chunk_images i
    SET source_page = COALESCE(
        (
            SELECT CASE
                WHEN COALESCE(entry.img->>'source_page', '') ~ '^[1-9][0-9]*$'
                THEN (entry.img->>'source_page')::int
            END
            FROM chunks c
            CROSS JOIN LATERAL jsonb_array_elements(chunk_images_try_jsonb(c.image_info))
                WITH ORDINALITY AS entry(img, idx)
            WHERE c.id = NEW.chunk_id AND entry.idx = NEW.image_index + 1
        ),
        (
            SELECT MIN((locator->>'page')::int)
            FROM chunks c
            CROSS JOIN LATERAL jsonb_array_elements(COALESCE(c.source_locators, '[]'::jsonb)) AS locator
            WHERE c.id = NEW.chunk_id
              AND locator->>'type' = 'pdf'
              AND (locator->>'page') ~ '^[1-9][0-9]*$'
        ),
        0
    )
    WHERE i.chunk_id = NEW.chunk_id AND i.image_index = NEW.image_index;
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS trg_chunk_images_source_page ON chunk_images;
CREATE TRIGGER trg_chunk_images_source_page
    AFTER INSERT ON chunk_images
    FOR EACH ROW EXECUTE FUNCTION chunk_images_source_page_sync();

-- Locator edits do not rebuild image_info, so refresh every projected row for
-- the changed chunk directly when a user edit invalidates or replaces source
-- positions.
CREATE OR REPLACE FUNCTION chunk_images_source_page_refresh_chunk() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE chunk_images i
    SET source_page = COALESCE(
        (
            SELECT CASE
                WHEN COALESCE(entry.img->>'source_page', '') ~ '^[1-9][0-9]*$'
                THEN (entry.img->>'source_page')::int
            END
            FROM jsonb_array_elements(chunk_images_try_jsonb(NEW.image_info))
                WITH ORDINALITY AS entry(img, idx)
            WHERE entry.idx = i.image_index + 1
        ),
        (
            SELECT MIN((locator->>'page')::int)
            FROM jsonb_array_elements(COALESCE(NEW.source_locators, '[]'::jsonb)) AS locator
            WHERE locator->>'type' = 'pdf'
              AND (locator->>'page') ~ '^[1-9][0-9]*$'
        ),
        0
    )
    WHERE i.chunk_id = NEW.id;
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS trg_chunk_images_source_page_locator_update ON chunks;
CREATE TRIGGER trg_chunk_images_source_page_locator_update
    AFTER UPDATE OF source_locators ON chunks
    FOR EACH ROW WHEN (OLD.source_locators IS DISTINCT FROM NEW.source_locators)
    EXECUTE FUNCTION chunk_images_source_page_refresh_chunk();