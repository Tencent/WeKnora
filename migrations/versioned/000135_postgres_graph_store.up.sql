-- PostgreSQL storage for the entity-relation knowledge graph.
--
-- The graph is scoped by (knowledge_base_id, knowledge_id), matching the
-- NameSpace the extraction and deletion paths already pass. A node is unique
-- per document and name, so re-extracting a document merges into the same
-- rows instead of duplicating the entity.
--
-- Chunk ids live in a child table rather than a JSON array: concurrent chunk
-- extractions add to the same entity, and INSERT ... ON CONFLICT DO NOTHING
-- makes that union atomic without dialect-specific array SQL.

CREATE TABLE IF NOT EXISTS graph_nodes (
    id                varchar(36) PRIMARY KEY,
    knowledge_base_id varchar(36) NOT NULL,
    knowledge_id      varchar(36) NOT NULL,
    name              text        NOT NULL,
    attributes        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT graph_nodes_scope_name_key UNIQUE (knowledge_base_id, knowledge_id, name)
);

CREATE INDEX IF NOT EXISTS idx_graph_nodes_kb_name
    ON graph_nodes (knowledge_base_id, name);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_kb
    ON graph_nodes (knowledge_base_id);

CREATE TABLE IF NOT EXISTS graph_node_chunks (
    node_id  varchar(36) NOT NULL REFERENCES graph_nodes (id) ON DELETE CASCADE,
    chunk_id varchar(64) NOT NULL,
    PRIMARY KEY (node_id, chunk_id)
);

CREATE TABLE IF NOT EXISTS graph_edges (
    id                varchar(36) PRIMARY KEY,
    knowledge_base_id varchar(36) NOT NULL,
    knowledge_id      varchar(36) NOT NULL,
    source_id         varchar(36) NOT NULL REFERENCES graph_nodes (id) ON DELETE CASCADE,
    target_id         varchar(36) NOT NULL REFERENCES graph_nodes (id) ON DELETE CASCADE,
    type              text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT graph_edges_unique UNIQUE (knowledge_base_id, knowledge_id, source_id, target_id, type)
);

CREATE INDEX IF NOT EXISTS idx_graph_edges_kb_source
    ON graph_edges (knowledge_base_id, source_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_kb_target
    ON graph_edges (knowledge_base_id, target_id);
