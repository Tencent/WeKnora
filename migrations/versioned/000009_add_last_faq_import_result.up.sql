-- Add last_faq_import_result column to knowledge table
-- This field stores the latest FAQ import result for FAQ type knowledge

ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS last_faq_import_result JSON DEFAULT NULL;