CREATE TABLE model_catalog_configs (
    id INTEGER PRIMARY KEY,
    version BIGINT NOT NULL DEFAULT 0,
    overlay TEXT NOT NULL,
    history TEXT NOT NULL,
    updated_by VARCHAR(36) NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (id = 1)
);
INSERT INTO model_catalog_configs (id, version, overlay, history)
VALUES (1, 0, '{"providers":{}}', '[]');
