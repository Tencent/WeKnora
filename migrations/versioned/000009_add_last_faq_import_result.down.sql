-- Remove last_faq_import_result column from knowledge table

ALTER TABLE knowledges DROP COLUMN IF EXISTS last_faq_import_result;