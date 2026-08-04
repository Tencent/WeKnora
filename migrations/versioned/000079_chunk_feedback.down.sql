-- Migration: 000079_chunk_feedback
-- Description: 回滚知识库反馈基础数据模型
-- Date: 2026-07-02

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Rolling back chunk feedback setup...'; END $$;

-- ============================================
-- 1. 回滚 messages 表的 feedback_count 字段
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Rolling back messages table...'; END $$;

ALTER TABLE messages DROP COLUMN IF EXISTS like_count;
ALTER TABLE messages DROP COLUMN IF EXISTS dislike_count;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] messages table rolled back'; END $$;

-- ============================================
-- 2. 删除 chunk_weight_logs 表
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Dropping chunk_weight_logs table...'; END $$;

DROP TABLE IF EXISTS chunk_weight_logs;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] chunk_weight_logs table dropped'; END $$;

-- ============================================
-- 3. 删除 chunk_feedbacks 表
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Dropping chunk_feedbacks table...'; END $$;

DROP TABLE IF EXISTS chunk_feedbacks;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] chunk_feedbacks table dropped'; END $$;

-- ============================================
-- 4. 删除 qa_reply_chunk_ref_tombstones 表
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Dropping qa_reply_chunk_ref_tombstones table...'; END $$;

DROP TABLE IF EXISTS qa_reply_chunk_ref_tombstones;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] qa_reply_chunk_ref_tombstones table dropped'; END $$;

-- ============================================
-- 5. 删除 qa_reply_chunk_refs 表
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Dropping qa_reply_chunk_refs table...'; END $$;

DROP TABLE IF EXISTS qa_reply_chunk_refs;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] qa_reply_chunk_refs table dropped'; END $$;

-- ============================================
-- 6. 回滚 chunks 表
-- ============================================
DO $$ BEGIN RAISE NOTICE '[Migration 000079] Rolling back chunks table...'; END $$;

ALTER TABLE chunks DROP COLUMN IF EXISTS like_count;
ALTER TABLE chunks DROP COLUMN IF EXISTS dislike_count;
ALTER TABLE chunks DROP COLUMN IF EXISTS positive_rate;
ALTER TABLE chunks DROP COLUMN IF EXISTS recall_weight;
ALTER TABLE chunks DROP COLUMN IF EXISTS quality_status;
ALTER TABLE chunks DROP COLUMN IF EXISTS dislike_reasons;
ALTER TABLE chunks DROP COLUMN IF EXISTS last_feedback_at;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] chunks table rolled back'; END $$;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Rollback completed successfully'; END $$;
