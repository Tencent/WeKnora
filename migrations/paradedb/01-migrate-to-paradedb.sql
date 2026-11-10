-- 迁移脚本：从PostgreSQL迁移到ParadeDB
-- 注意：在执行此脚本前，请确保已经备份了数据

-- 1. 导出数据（在PostgreSQL中执行）
-- pg_dump -U postgres -h localhost -p 5432 -d your_database > backup.sql

-- 2. 导入数据（在ParadeDB中执行）
-- psql -U postgres -h localhost -p 5432 -d your_database < backup.sql

-- 3. 验证数据


-- Insert some sample data
-- INSERT INTO tenants (id, name, description, status, api_key)
-- VALUES 
--     (1, 'Demo Tenant', 'This is a demo tenant for testing', 'active', 'sk-00000001abcdefg123456')
-- ON CONFLICT DO NOTHING;

-- SELECT setval('tenants_id_seq', (SELECT MAX(id) FROM tenants));


-- -- Create knowledge base
-- INSERT INTO knowledge_bases (id, name, description, tenant_id, chunking_config, image_processing_config, embedding_model_id)
-- VALUES 
--     ('kb-00000001', 'Default Knowledge Base', 'Default knowledge base for testing', 1, '{"chunk_size": 512, "chunk_overlap": 50, "separators": ["\n\n", "\n", "。"], "keep_separator": true}', '{"enable_multimodal": false, "model_id": ""}', 'model-embedding-00000001'),
--     ('kb-00000002', 'Test Knowledge Base', 'Test knowledge base for development', 1, '{"chunk_size": 512, "chunk_overlap": 50, "separators": ["\n\n", "\n", "。"], "keep_separator": true}', '{"enable_multimodal": false, "model_id": ""}', 'model-embedding-00000001'),
--     ('kb-00000003', 'Test Knowledge Base 2', 'Test knowledge base for development 2', 1, '{"chunk_size": 512, "chunk_overlap": 50, "separators": ["\n\n", "\n", "。"], "keep_separator": true}', '{"enable_multimodal": false, "model_id": ""}', 'model-embedding-00000001')
-- ON CONFLICT DO NOTHING;


SELECT COUNT(*) FROM tenants;
SELECT COUNT(*) FROM models;
SELECT COUNT(*) FROM knowledge_bases;
SELECT COUNT(*) FROM knowledges;


-- 测试中文全文搜索

-- 创建文档表
CREATE TABLE chinese_documents (
    id SERIAL PRIMARY KEY,
    title TEXT,
    content TEXT,
    published_date DATE
);

-- 在表上创建 BM25 索引，使用结巴分词器支持中文
CREATE INDEX idx_documents_bm25 ON chinese_documents
USING bm25 (id, content)
WITH (
    key_field = 'id',
    text_fields = '{
        "content": {
          "tokenizer": {"type": "chinese_lindera"}
        }
    }'
);

INSERT INTO chinese_documents (title, content, published_date)
VALUES 
('人工智能的发展', '人工智能技术正在快速发展，影响了我们生活的方方面面。大语言模型是最近的一个重要突破。', '2023-01-15'),
('机器学习基础', '机器学习是人工智能的一个重要分支，包括监督学习、无监督学习和强化学习等方法。', '2023-02-20'),
('深度学习应用', '深度学习在图像识别、自然语言处理和语音识别等领域有广泛应用。', '2023-03-10'),
('自然语言处理技术', '自然语言处理允许计算机理解、解释和生成人类语言，是人工智能的核心技术之一。', '2023-04-05'),
('计算机视觉入门', '计算机视觉让机器能够"看到"并理解视觉世界，广泛应用于安防、医疗等领域。', '2023-05-12');

INSERT INTO chinese_documents (title, content, published_date)
VALUES 
('hello world', 'hello world', '2023-05-12');
