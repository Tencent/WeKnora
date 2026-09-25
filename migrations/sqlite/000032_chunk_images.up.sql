-- Migration 000032: chunk_images, the image gallery's projection of image_info
--
-- SQLite twin of versioned migration 000113; see it for the rationale. One row
-- per image_info entry with caption, OCR text and observed attributes as
-- columns, kept in step with chunks by triggers, so the gallery no longer
-- parses every image chunk's JSON on each request. Copies of one image stay
-- separate rows; the gallery query picks the copy on the most recently updated
-- chunk with an indexed anti-join.

CREATE TABLE IF NOT EXISTS chunk_images (
    chunk_id VARCHAR(36) NOT NULL,
    image_index INTEGER NOT NULL,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_type VARCHAR(20) NOT NULL DEFAULT '',
    image_key TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    original_url TEXT NOT NULL DEFAULT '',
    caption TEXT NOT NULL DEFAULT '',
    ocr_text TEXT NOT NULL DEFAULT '',
    attrs TEXT NOT NULL DEFAULT '{}',
    is_enabled BOOLEAN NOT NULL DEFAULT 1,
    status INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME,
    updated_at DATETIME,
    PRIMARY KEY (chunk_id, image_index)
);

CREATE INDEX IF NOT EXISTS idx_chunk_images_listing
    ON chunk_images(knowledge_base_id, tenant_id, created_at, chunk_id, image_index);
CREATE INDEX IF NOT EXISTS idx_chunk_images_copies
    ON chunk_images(knowledge_base_id, tenant_id, image_key, updated_at, chunk_id, image_index);

CREATE TRIGGER IF NOT EXISTS trg_chunk_images_insert AFTER INSERT ON chunks
WHEN NEW.image_info LIKE '[%'
BEGIN
    INSERT INTO chunk_images (chunk_id, image_index, tenant_id, knowledge_base_id, knowledge_id, chunk_type, image_key, url, original_url, caption, ocr_text, attrs, is_enabled, status, created_at, updated_at)
    SELECT NEW.id, CAST(e.key AS INTEGER), NEW.tenant_id, NEW.knowledge_base_id, NEW.knowledge_id,
           COALESCE(NEW.chunk_type, ''),
           COALESCE(NULLIF(json_extract(e.value, '$.url'), ''), NULLIF(json_extract(e.value, '$.original_url'), ''),
                    NEW.id || '#' || e.key),
           COALESCE(json_extract(e.value, '$.url'), ''), COALESCE(json_extract(e.value, '$.original_url'), ''),
           COALESCE(json_extract(e.value, '$.caption'), ''), COALESCE(json_extract(e.value, '$.ocr_text'), ''),
           CASE WHEN json_type(e.value, '$.attrs.attrs') = 'object'
                THEN json_extract(e.value, '$.attrs.attrs') ELSE '{}' END,
           NEW.is_enabled, COALESCE(NEW.status, 0), NEW.created_at, NEW.updated_at
    FROM json_each(CASE WHEN json_valid(NEW.image_info) AND json_type(NEW.image_info) = 'array' THEN NEW.image_info ELSE '[]' END) AS e
    WHERE NEW.deleted_at IS NULL AND e.type = 'object';
END;

-- An update that leaves the images alone (status, enabled flag, timestamps)
-- patches the rows in place; any other change to a column the projection
-- copies replaces the chunk's rows.
CREATE TRIGGER IF NOT EXISTS trg_chunk_images_patch AFTER UPDATE ON chunks
WHEN NEW.image_info LIKE '[%' AND OLD.deleted_at IS NULL AND NEW.deleted_at IS NULL
    AND OLD.image_info IS NEW.image_info
    AND OLD.tenant_id IS NEW.tenant_id
    AND OLD.knowledge_base_id IS NEW.knowledge_base_id
    AND OLD.knowledge_id IS NEW.knowledge_id
    AND OLD.chunk_type IS NEW.chunk_type
    AND (OLD.is_enabled IS NOT NEW.is_enabled OR OLD.status IS NOT NEW.status
         OR OLD.created_at IS NOT NEW.created_at OR OLD.updated_at IS NOT NEW.updated_at)
BEGIN
    UPDATE chunk_images
    SET is_enabled = NEW.is_enabled, status = COALESCE(NEW.status, 0),
        created_at = NEW.created_at, updated_at = NEW.updated_at
    WHERE chunk_id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trg_chunk_images_update AFTER UPDATE ON chunks
WHEN (OLD.image_info LIKE '[%' OR NEW.image_info LIKE '[%') AND NOT (OLD.deleted_at IS NULL AND NEW.deleted_at IS NULL
    AND OLD.image_info IS NEW.image_info
    AND OLD.tenant_id IS NEW.tenant_id
    AND OLD.knowledge_base_id IS NEW.knowledge_base_id
    AND OLD.knowledge_id IS NEW.knowledge_id
    AND OLD.chunk_type IS NEW.chunk_type)
BEGIN
    DELETE FROM chunk_images WHERE chunk_id = OLD.id;
    INSERT INTO chunk_images (chunk_id, image_index, tenant_id, knowledge_base_id, knowledge_id, chunk_type, image_key, url, original_url, caption, ocr_text, attrs, is_enabled, status, created_at, updated_at)
    SELECT NEW.id, CAST(e.key AS INTEGER), NEW.tenant_id, NEW.knowledge_base_id, NEW.knowledge_id,
           COALESCE(NEW.chunk_type, ''),
           COALESCE(NULLIF(json_extract(e.value, '$.url'), ''), NULLIF(json_extract(e.value, '$.original_url'), ''),
                    NEW.id || '#' || e.key),
           COALESCE(json_extract(e.value, '$.url'), ''), COALESCE(json_extract(e.value, '$.original_url'), ''),
           COALESCE(json_extract(e.value, '$.caption'), ''), COALESCE(json_extract(e.value, '$.ocr_text'), ''),
           CASE WHEN json_type(e.value, '$.attrs.attrs') = 'object'
                THEN json_extract(e.value, '$.attrs.attrs') ELSE '{}' END,
           NEW.is_enabled, COALESCE(NEW.status, 0), NEW.created_at, NEW.updated_at
    FROM json_each(CASE WHEN json_valid(NEW.image_info) AND json_type(NEW.image_info) = 'array' THEN NEW.image_info ELSE '[]' END) AS e
    WHERE NEW.deleted_at IS NULL AND e.type = 'object';
END;

CREATE TRIGGER IF NOT EXISTS trg_chunk_images_delete AFTER DELETE ON chunks
WHEN OLD.image_info LIKE '[%'
BEGIN
    DELETE FROM chunk_images WHERE chunk_id = OLD.id;
END;

-- Backfill every live image chunk.
INSERT OR IGNORE INTO chunk_images (chunk_id, image_index, tenant_id, knowledge_base_id, knowledge_id, chunk_type, image_key, url, original_url, caption, ocr_text, attrs, is_enabled, status, created_at, updated_at)
    SELECT c.id, CAST(e.key AS INTEGER), c.tenant_id, c.knowledge_base_id, c.knowledge_id,
           COALESCE(c.chunk_type, ''),
           COALESCE(NULLIF(json_extract(e.value, '$.url'), ''), NULLIF(json_extract(e.value, '$.original_url'), ''),
                    c.id || '#' || e.key),
           COALESCE(json_extract(e.value, '$.url'), ''), COALESCE(json_extract(e.value, '$.original_url'), ''),
           COALESCE(json_extract(e.value, '$.caption'), ''), COALESCE(json_extract(e.value, '$.ocr_text'), ''),
           CASE WHEN json_type(e.value, '$.attrs.attrs') = 'object'
                THEN json_extract(e.value, '$.attrs.attrs') ELSE '{}' END,
           c.is_enabled, COALESCE(c.status, 0), c.created_at, c.updated_at
    FROM chunks c, json_each(CASE WHEN json_valid(c.image_info) AND json_type(c.image_info) = 'array' THEN c.image_info ELSE '[]' END) AS e
    WHERE c.deleted_at IS NULL AND e.type = 'object' AND c.image_info LIKE '[%';
