-- Migration 000113: chunk_images, the image gallery's projection of image_info
--
-- The gallery lists a knowledge base's images: one per URL, filtered by
-- caption / OCR text / observed attributes, sorted and paged. That data lives
-- as JSON inside chunks.image_info (one array per chunk, the same image
-- repeated on its image_ocr and image_caption children), so answering a page
-- from chunks means parsing every image chunk of the knowledge base.
--
-- chunk_images keeps one row per image_info entry with the fields split into
-- columns, and triggers on chunks keep it in step with every write path
-- (insert, edit, enable toggle, soft and hard delete, moving a document). It is
-- a plain projection: copies of one image stay separate rows, and the gallery
-- query picks the copy on the most recently updated chunk with an indexed
-- anti-join. Nothing is decided at write time, so concurrent writers of one
-- image need no coordination.

CREATE TABLE IF NOT EXISTS chunk_images (
    chunk_id VARCHAR(36) NOT NULL,
    image_index INTEGER NOT NULL,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_type VARCHAR(20) NOT NULL DEFAULT '',
    -- url, else original_url, else "<chunk id>#<index>": what de-duplicates.
    image_key TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    original_url TEXT NOT NULL DEFAULT '',
    caption TEXT NOT NULL DEFAULT '',
    ocr_text TEXT NOT NULL DEFAULT '',
    -- The observed attribute map (image_info[i].attrs.attrs).
    attrs JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_enabled BOOLEAN NOT NULL DEFAULT true,
    status INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE,
    PRIMARY KEY (chunk_id, image_index)
);

-- Listing in upload order, ties by chunk and position.
CREATE INDEX IF NOT EXISTS idx_chunk_images_listing
    ON chunk_images(knowledge_base_id, tenant_id, created_at, chunk_id, image_index);
-- The copies of one image, in the order that decides which one is listed.
CREATE INDEX IF NOT EXISTS idx_chunk_images_copies
    ON chunk_images(knowledge_base_id, tenant_id, image_key, updated_at, chunk_id, image_index);

-- chunk_images_try_jsonb parses image_info, or returns NULL for malformed
-- text so one bad row can neither fail a chunk write nor this backfill.
CREATE OR REPLACE FUNCTION chunk_images_try_jsonb(raw TEXT) RETURNS JSONB
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
    IF raw IS NULL OR raw NOT LIKE '[%' THEN
        RETURN NULL;
    END IF;
    RETURN raw::jsonb;
EXCEPTION WHEN others THEN
    RETURN NULL;
END $$;

-- chunk_images_sync re-projects one chunk. An update that leaves the images
-- alone (status, enabled flag, timestamps) patches the rows in place; none of
-- those columns is text-indexed, so it costs no index churn. Anything else
-- replaces the chunk's rows.
CREATE OR REPLACE FUNCTION chunk_images_sync() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    doc JSONB;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.deleted_at IS NULL AND NEW.deleted_at IS NULL
       AND OLD.image_info IS NOT DISTINCT FROM NEW.image_info
       AND OLD.tenant_id = NEW.tenant_id AND OLD.knowledge_base_id = NEW.knowledge_base_id
       AND OLD.knowledge_id = NEW.knowledge_id AND OLD.chunk_type IS NOT DISTINCT FROM NEW.chunk_type THEN
        UPDATE chunk_images
        SET is_enabled = NEW.is_enabled, status = COALESCE(NEW.status, 0),
            created_at = NEW.created_at, updated_at = NEW.updated_at
        WHERE chunk_id = NEW.id;
        RETURN NULL;
    END IF;

    IF TG_OP <> 'INSERT' THEN
        DELETE FROM chunk_images WHERE chunk_id = OLD.id;
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.deleted_at IS NULL THEN
        doc := chunk_images_try_jsonb(NEW.image_info);
        IF jsonb_typeof(doc) = 'array' THEN
            INSERT INTO chunk_images (
                chunk_id, image_index, tenant_id, knowledge_base_id, knowledge_id, chunk_type,
                image_key, url, original_url, caption, ocr_text, attrs,
                is_enabled, status, created_at, updated_at
            )
            SELECT NEW.id, (e.idx - 1)::int, NEW.tenant_id, NEW.knowledge_base_id, NEW.knowledge_id,
                   COALESCE(NEW.chunk_type, ''),
                   COALESCE(NULLIF(e.img->>'url', ''), NULLIF(e.img->>'original_url', ''),
                            NEW.id || '#' || (e.idx - 1)),
                   COALESCE(e.img->>'url', ''), COALESCE(e.img->>'original_url', ''),
                   COALESCE(e.img->>'caption', ''), COALESCE(e.img->>'ocr_text', ''),
                   CASE WHEN jsonb_typeof(e.img->'attrs'->'attrs') = 'object'
                        THEN e.img->'attrs'->'attrs' ELSE '{}'::jsonb END,
                   NEW.is_enabled, COALESCE(NEW.status, 0), NEW.created_at, NEW.updated_at
            FROM jsonb_array_elements(doc) WITH ORDINALITY AS e(img, idx)
            WHERE jsonb_typeof(e.img) = 'object';
        END IF;
    END IF;
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS trg_chunk_images_insert ON chunks;
CREATE TRIGGER trg_chunk_images_insert AFTER INSERT ON chunks
    FOR EACH ROW WHEN (NEW.image_info LIKE '[%')
    EXECUTE FUNCTION chunk_images_sync();

-- Only the columns the projection copies re-sync it; a chunk whose image_info
-- neither was nor becomes an array has nothing to project.
DROP TRIGGER IF EXISTS trg_chunk_images_update ON chunks;
CREATE TRIGGER trg_chunk_images_update AFTER UPDATE ON chunks
    FOR EACH ROW WHEN (
        (OLD.image_info LIKE '[%' OR NEW.image_info LIKE '[%') AND (
            OLD.image_info IS DISTINCT FROM NEW.image_info
            OR OLD.is_enabled IS DISTINCT FROM NEW.is_enabled
            OR OLD.status IS DISTINCT FROM NEW.status
            OR OLD.deleted_at IS DISTINCT FROM NEW.deleted_at
            OR OLD.tenant_id IS DISTINCT FROM NEW.tenant_id
            OR OLD.knowledge_base_id IS DISTINCT FROM NEW.knowledge_base_id
            OR OLD.knowledge_id IS DISTINCT FROM NEW.knowledge_id
            OR OLD.chunk_type IS DISTINCT FROM NEW.chunk_type
            OR OLD.created_at IS DISTINCT FROM NEW.created_at
            OR OLD.updated_at IS DISTINCT FROM NEW.updated_at
        )
    )
    EXECUTE FUNCTION chunk_images_sync();

DROP TRIGGER IF EXISTS trg_chunk_images_delete ON chunks;
CREATE TRIGGER trg_chunk_images_delete AFTER DELETE ON chunks
    FOR EACH ROW WHEN (OLD.image_info LIKE '[%')
    EXECUTE FUNCTION chunk_images_sync();

-- Backfill every live image chunk.
INSERT INTO chunk_images (
    chunk_id, image_index, tenant_id, knowledge_base_id, knowledge_id, chunk_type,
    image_key, url, original_url, caption, ocr_text, attrs,
    is_enabled, status, created_at, updated_at
)
SELECT c.id, (e.idx - 1)::int, c.tenant_id, c.knowledge_base_id, c.knowledge_id, COALESCE(c.chunk_type, ''),
       COALESCE(NULLIF(e.img->>'url', ''), NULLIF(e.img->>'original_url', ''), c.id || '#' || (e.idx - 1)),
       COALESCE(e.img->>'url', ''), COALESCE(e.img->>'original_url', ''),
       COALESCE(e.img->>'caption', ''), COALESCE(e.img->>'ocr_text', ''),
       CASE WHEN jsonb_typeof(e.img->'attrs'->'attrs') = 'object'
            THEN e.img->'attrs'->'attrs' ELSE '{}'::jsonb END,
       c.is_enabled, COALESCE(c.status, 0), c.created_at, c.updated_at
FROM chunks c
CROSS JOIN LATERAL jsonb_array_elements(
    CASE WHEN jsonb_typeof(chunk_images_try_jsonb(c.image_info)) = 'array'
         THEN chunk_images_try_jsonb(c.image_info) ELSE '[]'::jsonb END
) WITH ORDINALITY AS e(img, idx)
WHERE c.deleted_at IS NULL AND c.image_info LIKE '[%' AND jsonb_typeof(e.img) = 'object'
ON CONFLICT (chunk_id, image_index) DO NOTHING;

-- Keyword search over caption / OCR text uses trigram indexes when pg_trgm is
-- available. It is optional: without it (no permission to create extensions)
-- the search still works, as a scan. fastupdate is off so a bulk import is
-- searchable at full speed at once instead of after the next vacuum merges
-- the pending list.
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
EXCEPTION WHEN others THEN
    RAISE NOTICE '[Migration 000113] pg_trgm unavailable (%); gallery keyword search will scan', SQLERRM;
END $$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        CREATE INDEX IF NOT EXISTS idx_chunk_images_caption_trgm
            ON chunk_images USING gin (caption gin_trgm_ops) WITH (fastupdate = off);
        CREATE INDEX IF NOT EXISTS idx_chunk_images_ocr_trgm
            ON chunk_images USING gin (ocr_text gin_trgm_ops) WITH (fastupdate = off);
    END IF;
END $$;
