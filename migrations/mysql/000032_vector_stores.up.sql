-- MySQL 8 translation of 000032_vector_stores.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE TABLE vector_stores (
    id                VARCHAR(36)  NOT NULL PRIMARY KEY,
    name              VARCHAR(255) NOT NULL,
    engine_type       VARCHAR(50)  NOT NULL,
    connection_config JSON        NOT NULL,
    index_config      JSON        NOT NULL,
    tenant_id         BIGINT       NOT NULL,
    created_at        TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
    deleted_at        TIMESTAMP    NULL
);
CREATE INDEX idx_vector_stores_tenant_id ON vector_stores(tenant_id);
CREATE INDEX idx_vector_stores_engine_type ON vector_stores(engine_type);
CREATE INDEX idx_vector_stores_deleted_at ON vector_stores(deleted_at);
