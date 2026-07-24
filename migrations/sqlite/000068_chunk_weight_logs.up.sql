CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chunk_id VARCHAR(36) NOT NULL REFERENCES chunks(id),
    old_recall_weight REAL NOT NULL DEFAULT 1,
    new_recall_weight REAL NOT NULL DEFAULT 1,
    old_quality_status VARCHAR(32) NOT NULL DEFAULT 'normal',
    new_quality_status VARCHAR(32) NOT NULL DEFAULT 'normal',
    old_positive_rate REAL NOT NULL DEFAULT 0,
    new_positive_rate REAL NOT NULL DEFAULT 0,
    old_like_count INTEGER NOT NULL DEFAULT 0,
    new_like_count INTEGER NOT NULL DEFAULT 0,
    old_dislike_count INTEGER NOT NULL DEFAULT 0,
    new_dislike_count INTEGER NOT NULL DEFAULT 0,
    triggered_by VARCHAR(64) NOT NULL DEFAULT 'feedback',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_chunk_id ON chunk_weight_logs(chunk_id);
