-- Rollback migration: 000029_web_search_providers
DROP TRIGGER IF EXISTS trg_web_search_providers_updated_at ON web_search_providers;
DROP TABLE IF EXISTS web_search_providers;
