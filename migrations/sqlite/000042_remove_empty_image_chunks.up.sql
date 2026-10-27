-- SQLite counterpart of PostgreSQL migration 000125. The SHA-256 of empty
-- input proves these single-image derived chunks never had image bytes.
-- Do not infer corruption from caption wording or remove source documents.
-- Soft deletion also removes chunk_images through its existing trigger.
UPDATE chunks
SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE deleted_at IS NULL
  AND chunk_type IN ('image_caption', 'image_ocr', 'image_vector')
  AND CASE WHEN json_valid(image_info) THEN
      CASE WHEN json_type(image_info) = 'array' THEN
          json_array_length(image_info) = 1
          AND json_extract(image_info, '$[0].sha256') =
              'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
      ELSE 0 END
  ELSE 0 END;
