-- Migration: Create "未分类" tag for each knowledge base that has untagged entries
-- and update chunks, knowledges, and embeddings to reference the new tag
ALTER EXTENSION pg_search UPDATE;

DO $$
DECLARE
    kb_record RECORD;
    new_tag_id VARCHAR(36);
    updated_chunks INT;
    updated_knowledges INT;
    updated_embeddings INT;
BEGIN
    -- Find all knowledge bases that have untagged chunks or knowledges
    FOR kb_record IN 
        SELECT DISTINCT tenant_id, knowledge_base_id 
        FROM (
            -- Untagged chunks (FAQ or document)
            SELECT c.tenant_id, c.knowledge_base_id 
            FROM chunks c
            WHERE c.chunk_type IN ('faq', 'document')
            AND (c.tag_id = '' OR c.tag_id IS NULL)
            UNION
            -- Untagged knowledges (documents)
            SELECT k.tenant_id, k.knowledge_base_id 
            FROM knowledges k
            WHERE k.deleted_at IS NULL
            AND (k.tag_id = '' OR k.tag_id IS NULL)
        ) AS untagged
    LOOP
        -- Check if "未分类" tag already exists for this knowledge base
        SELECT id INTO new_tag_id
        FROM knowledge_tags
        WHERE tenant_id = kb_record.tenant_id 
        AND knowledge_base_id = kb_record.knowledge_base_id 
        AND name = '未分类'
        LIMIT 1;

        -- If not exists, create the tag
        IF new_tag_id IS NULL THEN
            new_tag_id := gen_random_uuid()::VARCHAR(36);
            INSERT INTO knowledge_tags (id, tenant_id, knowledge_base_id, name, color, sort_order, created_at, updated_at)
            VALUES (new_tag_id, kb_record.tenant_id, kb_record.knowledge_base_id, '未分类', '', 0, NOW(), NOW());
            RAISE NOTICE '[Migration 000008] Created "未分类" tag (id: %) for tenant_id: %, kb_id: %', 
                new_tag_id, kb_record.tenant_id, kb_record.knowledge_base_id;
        ELSE
            RAISE NOTICE '[Migration 000008] "未分类" tag already exists (id: %) for tenant_id: %, kb_id: %', 
                new_tag_id, kb_record.tenant_id, kb_record.knowledge_base_id;
        END IF;

        -- Update chunks with empty tag_id (both faq and document types)
        UPDATE chunks 
        SET tag_id = new_tag_id, updated_at = NOW()
        WHERE tenant_id = kb_record.tenant_id 
        AND knowledge_base_id = kb_record.knowledge_base_id 
        AND chunk_type IN ('faq', 'document')
        AND (tag_id = '' OR tag_id IS NULL);
        
        GET DIAGNOSTICS updated_chunks = ROW_COUNT;
        RAISE NOTICE '[Migration 000008] Updated % chunks for tenant_id: %, kb_id: %', 
            updated_chunks, kb_record.tenant_id, kb_record.knowledge_base_id;

        -- Update knowledges with empty tag_id (documents)
        UPDATE knowledges 
        SET tag_id = new_tag_id, updated_at = NOW()
        WHERE tenant_id = kb_record.tenant_id 
        AND knowledge_base_id = kb_record.knowledge_base_id 
        AND deleted_at IS NULL
        AND (tag_id = '' OR tag_id IS NULL);
        
        GET DIAGNOSTICS updated_knowledges = ROW_COUNT;
        RAISE NOTICE '[Migration 000008] Updated % knowledges for tenant_id: %, kb_id: %', 
            updated_knowledges, kb_record.tenant_id, kb_record.knowledge_base_id;

        -- Update embeddings with empty tag_id (if embeddings table exists and has tag_id column)
        IF EXISTS (
            SELECT 1 FROM information_schema.columns 
            WHERE table_name = 'embeddings' AND column_name = 'tag_id'
        ) THEN
            UPDATE embeddings 
            SET tag_id = new_tag_id
            WHERE knowledge_base_id = kb_record.knowledge_base_id 
            AND (tag_id = '' OR tag_id IS NULL)
            AND chunk_id IN (
                SELECT id FROM chunks 
                WHERE tenant_id = kb_record.tenant_id 
                AND knowledge_base_id = kb_record.knowledge_base_id 
                AND chunk_type IN ('faq', 'document')
            );
            
            GET DIAGNOSTICS updated_embeddings = ROW_COUNT;
            RAISE NOTICE '[Migration 000008] Updated % embeddings for kb_id: %', 
                updated_embeddings, kb_record.knowledge_base_id;
        END IF;
    END LOOP;

    RAISE NOTICE '[Migration 000008] Completed migration of untagged entries';
END $$;
