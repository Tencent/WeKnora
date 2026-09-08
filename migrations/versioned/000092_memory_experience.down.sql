DELETE FROM memory_item_embeddings WHERE item_id IN (SELECT id FROM memory_items WHERE kind = 'experience');
DELETE FROM memory_items WHERE kind = 'experience';
ALTER TABLE memory_items DROP COLUMN experience;
ALTER TABLE memory_subjects DROP COLUMN extraction_progress;
ALTER TABLE memory_subjects DROP COLUMN extract_lease_token;
ALTER TABLE memory_subjects DROP COLUMN extract_lease_until;
