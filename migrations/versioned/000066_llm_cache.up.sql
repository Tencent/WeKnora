CREATE TABLE IF NOT EXISTS llm_cache (
    cache_key    VARCHAR(64) PRIMARY KEY,
    model_id     VARCHAR(255) NOT NULL DEFAULT '',
    prompt_hash  VARCHAR(32) NOT NULL DEFAULT '',
    result       TEXT NOT NULL,
    text_preview TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_llm_cache_model_id ON llm_cache(model_id);
CREATE INDEX IF NOT EXISTS idx_llm_cache_prompt_hash ON llm_cache(prompt_hash);
