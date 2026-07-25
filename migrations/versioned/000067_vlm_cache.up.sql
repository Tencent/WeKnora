CREATE TABLE IF NOT EXISTS vlm_cache (
    cache_key    VARCHAR(64) PRIMARY KEY,
    model_id     VARCHAR(255) NOT NULL DEFAULT '',
    image_hash   VARCHAR(64) NOT NULL DEFAULT '',
    prompt_hash  VARCHAR(32) NOT NULL DEFAULT '',
    result       TEXT NOT NULL,
    text_preview TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_vlm_cache_model_id ON vlm_cache(model_id);
CREATE INDEX IF NOT EXISTS idx_vlm_cache_image_hash ON vlm_cache(image_hash);
