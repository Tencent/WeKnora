-- Rollback migration: Remove chunk feedback statistics and message-chunk relations

-- 1. 删除配置表
DROP TABLE IF EXISTS `chunk_feedback_config`;

-- 2. 删除权重变更日志表
DROP TABLE IF EXISTS `chunk_weight_logs`;

-- 3. 删除用户评价记录表
DROP TABLE IF EXISTS `chunk_feedbacks`;

-- 4. 删除问答回复-片段关联表
DROP TABLE IF EXISTS `message_chunk_relations`;

-- 5. 删除 chunks 表的统计字段
ALTER TABLE `chunks` 
    DROP COLUMN IF EXISTS `is_pending_optimization`,
    DROP COLUMN IF EXISTS `recall_weight`,
    DROP COLUMN IF EXISTS `like_rate`,
    DROP COLUMN IF EXISTS `dislike_count`,
    DROP COLUMN IF EXISTS `like_count`;

-- 删除相关索引
ALTER TABLE `chunks` 
    DROP INDEX IF EXISTS `idx_chunks_pending_optimization`,
    DROP INDEX IF EXISTS `idx_chunks_recall_weight`,
    DROP INDEX IF EXISTS `idx_chunks_like_rate`;
