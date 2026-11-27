-- Wiki list keyword filtering uses substring matching. Source reference lookups
-- combine JSONB containment with a text LIKE predicate for legacy references.
-- These GIN indexes do not serve those queries, but add write overhead.
DROP INDEX IF EXISTS idx_wiki_pages_fulltext;
DROP INDEX IF EXISTS idx_wiki_pages_source_refs;
DROP INDEX IF EXISTS idx_wiki_pages_source_refs_text;
