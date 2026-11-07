-- Empty storage reads used to reach the VLM as text-only requests, allowing
-- "please provide an image" to be saved as a successful caption. The persisted
-- SHA-256 identifies that input exactly; caption wording is not evidence.
-- Limit cleanup to single-image derived chunks, leaving source text, unknown
-- fingerprints, mixed-image chunks, and physical storage untouched.
-- Soft deletion follows the normal chunk deletion path. Existing chunk_images
-- triggers remove the gallery projection and retrieval ignores deleted chunks.
UPDATE chunks
SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE deleted_at IS NULL
  AND chunk_type IN ('image_caption', 'image_ocr', 'image_vector')
  AND CASE
      WHEN jsonb_typeof(chunk_images_try_jsonb(image_info)) = 'array' THEN
          jsonb_array_length(chunk_images_try_jsonb(image_info)) = 1
          AND chunk_images_try_jsonb(image_info)->0->>'sha256' =
              'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
      ELSE false
  END;
