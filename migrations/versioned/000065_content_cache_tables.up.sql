-- Content-addressed reparse cache: canonical outputs for VLM, embeddings,
-- wiki map, graph chunk extraction, and parse products.
DO $$ BEGIN RAISE NOTICE '[Migration 000065] Creating content-addressed reparse cache tables...'; END $$;

CREATE TABLE IF NOT EXISTS vlm_cache (
    image_hash VARCHAR(64) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    prompt_kind VARCHAR(32) NOT NULL,
    output_text TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (image_hash, model_id, prompt_version, prompt_kind)
);

CREATE TABLE IF NOT EXISTS embedding_cache (
    text_hash VARCHAR(128) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    dimension INTEGER NOT NULL,
    vector TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (text_hash, model_id, dimension)
);

CREATE TABLE IF NOT EXISTS wiki_map_cache (
    doc_content_hash VARCHAR(128) NOT NULL,
    granularity VARCHAR(32) NOT NULL,
    synthesis_model_id VARCHAR(64) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    payload TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (doc_content_hash, granularity, synthesis_model_id, prompt_version)
);

CREATE TABLE IF NOT EXISTS graph_chunk_cache (
    chunk_content_hash VARCHAR(128) NOT NULL,
    extract_config_hash VARCHAR(128) NOT NULL,
    chat_model_id VARCHAR(64) NOT NULL,
    prompt_version VARCHAR(64) NOT NULL,
    payload TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (chunk_content_hash, extract_config_hash, chat_model_id, prompt_version)
);

CREATE TABLE IF NOT EXISTS parse_product_cache (
    file_hash VARCHAR(128) NOT NULL,
    parser_engine VARCHAR(32) NOT NULL,
    parser_config_hash VARCHAR(128) NOT NULL,
    render_config_hash VARCHAR(128) NOT NULL,
    payload TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (file_hash, parser_engine, parser_config_hash, render_config_hash)
);

DO $$ BEGIN RAISE NOTICE '[Migration 000065] content-addressed reparse cache tables ready'; END $$;
