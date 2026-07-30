-- Migration: 000079_chunk_feedback
-- Description: 添加知识库反馈基础数据模型
-- Date: 2026-07-02
-- Feature: 知识库问答-点赞点踩功能

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Starting chunk feedback setup...'; END $$;

-- ============================================
-- 1. 扩展 chunks 表 - 新增反馈统计字段
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Extending chunks table with feedback fields...'; END $$;

ALTER TABLE chunks ADD COLUMN IF NOT EXISTS like_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS dislike_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS positive_rate FLOAT NOT NULL DEFAULT 0.0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS recall_weight FLOAT NOT NULL DEFAULT 1.0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS quality_status VARCHAR(50) NOT NULL DEFAULT 'normal';
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS dislike_reasons JSONB DEFAULT '[]';
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS last_feedback_at TIMESTAMP WITH TIME ZONE;

-- 添加索引以支持按质量状态和好评率查询
CREATE INDEX IF NOT EXISTS idx_chunks_quality_status ON chunks(quality_status);
CREATE INDEX IF NOT EXISTS idx_chunks_positive_rate ON chunks(positive_rate);
CREATE INDEX IF NOT EXISTS idx_chunks_recall_weight ON chunks(recall_weight);
CREATE INDEX IF NOT EXISTS idx_chunks_last_feedback_at ON chunks(last_feedback_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Chunks table extended successfully'; END $$;

-- ============================================
-- 2. 创建 qa_reply_chunk_refs 表 - 问答回复与片段关联表
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Creating qa_reply_chunk_refs table...'; END $$;

CREATE TABLE IF NOT EXISTS qa_reply_chunk_refs (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    chunk_tenant_id INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_message_chunk UNIQUE(tenant_id, message_id, chunk_id, chunk_tenant_id),
    CONSTRAINT fk_qa_reply_chunk_refs_message FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE,
    CONSTRAINT fk_qa_reply_chunk_refs_chunk FOREIGN KEY(chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_refs_message_id ON qa_reply_chunk_refs(message_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_refs_chunk_id ON qa_reply_chunk_refs(chunk_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_refs_tenant_id ON qa_reply_chunk_refs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_refs_chunk_tenant_id ON qa_reply_chunk_refs(chunk_tenant_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000079] qa_reply_chunk_refs table created successfully'; END $$;

-- ============================================
-- 3. 创建 qa_reply_chunk_ref_tombstones 表 - 管理员重置后的历史关联屏蔽表
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Creating qa_reply_chunk_ref_tombstones table...'; END $$;

CREATE TABLE IF NOT EXISTS qa_reply_chunk_ref_tombstones (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    chunk_tenant_id INTEGER NOT NULL,
    operator VARCHAR(36),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_message_chunk_tombstone UNIQUE(tenant_id, message_id, chunk_id, chunk_tenant_id),
    CONSTRAINT fk_qa_reply_chunk_ref_tombstones_message FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE,
    CONSTRAINT fk_qa_reply_chunk_ref_tombstones_chunk FOREIGN KEY(chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_ref_tombstones_message_id ON qa_reply_chunk_ref_tombstones(message_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_ref_tombstones_chunk_id ON qa_reply_chunk_ref_tombstones(chunk_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_ref_tombstones_tenant_id ON qa_reply_chunk_ref_tombstones(tenant_id);
CREATE INDEX IF NOT EXISTS idx_qa_reply_chunk_ref_tombstones_chunk_tenant_id ON qa_reply_chunk_ref_tombstones(chunk_tenant_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000079] qa_reply_chunk_ref_tombstones table created successfully'; END $$;

-- ============================================
-- 4. 创建 chunk_feedbacks 表 - 用户评价记录表
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Creating chunk_feedbacks table...'; END $$;

CREATE TABLE IF NOT EXISTS chunk_feedbacks (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    message_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(512),
    is_positive BOOLEAN NOT NULL DEFAULT true,
    dislike_reason VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_message_user UNIQUE(tenant_id, message_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_message_id ON chunk_feedbacks(message_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_session_id ON chunk_feedbacks(session_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_tenant_id ON chunk_feedbacks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_user_id ON chunk_feedbacks(user_id);
CREATE INDEX IF NOT EXISTS idx_chunk_feedbacks_created_at ON chunk_feedbacks(created_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000079] chunk_feedbacks table created successfully'; END $$;

-- ============================================
-- 5. 创建 chunk_weight_logs 表 - 权重变更日志表
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Creating chunk_weight_logs table...'; END $$;

CREATE TABLE IF NOT EXISTS chunk_weight_logs (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    chunk_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    action VARCHAR(50) NOT NULL,
    old_weight FLOAT NOT NULL DEFAULT 1.0,
    new_weight FLOAT NOT NULL DEFAULT 1.0,
    trigger_type VARCHAR(50) NOT NULL,
    trigger_detail VARCHAR(500),
    operator VARCHAR(36),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_chunk_id ON chunk_weight_logs(chunk_id);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_tenant_id ON chunk_weight_logs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_action ON chunk_weight_logs(action);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_trigger_type ON chunk_weight_logs(trigger_type);
CREATE INDEX IF NOT EXISTS idx_chunk_weight_logs_created_at ON chunk_weight_logs(created_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000079] chunk_weight_logs table created successfully'; END $$;

-- ============================================
-- 6. 添加 message 表的 feedback_count 字段
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Adding feedback_count to messages table...'; END $$;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS like_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS dislike_count INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_messages_feedback ON messages(like_count, dislike_count);

DO $$ BEGIN RAISE NOTICE '[Migration 000079] messages table updated successfully'; END $$;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Chunk feedback setup completed successfully'; END $$;
