-- Mirror of versioned 000095. SQLite has no vector type, so ranking stays in
-- the application here; what it can share is the index that keeps the read to
-- one subject's vectors instead of the whole table.
CREATE INDEX IF NOT EXISTS idx_mem_emb_search
    ON memory_item_embeddings (tenant_id, subject_id, model_id, dims);
