-- Add index on created_at for TTL cleanup queries
CREATE INDEX IF NOT EXISTS idx_embedding_cache_created_at ON embedding_cache(created_at);
CREATE INDEX IF NOT EXISTS idx_llm_cache_created_at ON llm_cache(created_at);
CREATE INDEX IF NOT EXISTS idx_vlm_cache_created_at ON vlm_cache(created_at);
