-- Migration: Add chunk feedback statistics and message-chunk relations
-- Description: 新增知识库片段的用户反馈统计功能和问答回复-片段关联关系

-- ============================================================================
-- 1. 在 chunks 表新增统计字段
-- ============================================================================
ALTER TABLE `chunks` ADD COLUMN IF NOT EXISTS `like_count` INT NOT NULL DEFAULT 0 COMMENT '点赞数';
ALTER TABLE `chunks` ADD COLUMN IF NOT EXISTS `dislike_count` INT NOT NULL DEFAULT 0 COMMENT '点踩数';
ALTER TABLE `chunks` ADD COLUMN IF NOT EXISTS `like_rate` DECIMAL(5,4) NOT NULL DEFAULT 0.0000 COMMENT '好评率 (点赞/(点赞+点踩))';
ALTER TABLE `chunks` ADD COLUMN IF NOT EXISTS `recall_weight` DECIMAL(5,2) NOT NULL DEFAULT 1.00 COMMENT '召回权重 (默认1.0)';
ALTER TABLE `chunks` ADD COLUMN IF NOT EXISTS `is_pending_optimization` TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否标记为待优化 (好评率低于阈值时自动标记)';

-- 新增索引用于统计查询
ALTER TABLE `chunks` ADD INDEX IF NOT EXISTS `idx_chunks_like_rate` (`tenant_id`, `like_rate`);
ALTER TABLE `chunks` ADD INDEX IF NOT EXISTS `idx_chunks_recall_weight` (`tenant_id`, `recall_weight`);
ALTER TABLE `chunks` ADD INDEX IF NOT EXISTS `idx_chunks_pending_optimization` (`tenant_id`, `is_pending_optimization`);

-- ============================================================================
-- 2. 创建问答回复-片段关联表 (message_chunk_relations)
-- ============================================================================
CREATE TABLE IF NOT EXISTS `message_chunk_relations` (
    `id` VARCHAR(36) PRIMARY KEY,
    `tenant_id` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `message_id` VARCHAR(36) NOT NULL COMMENT '问答回复消息ID',
    `session_id` VARCHAR(36) NOT NULL COMMENT '所属会话ID',
    `chunk_id` VARCHAR(36) NOT NULL COMMENT '关联的知识库片段ID',
    `knowledge_id` VARCHAR(36) NOT NULL COMMENT '所属知识ID',
    `knowledge_base_id` VARCHAR(36) NOT NULL COMMENT '所属知识库ID',
    `score` DECIMAL(10,6) DEFAULT NULL COMMENT '检索时相似度分数',
    `created_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    `updated_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    `deleted_at` DATETIME DEFAULT NULL,
    INDEX `idx_mcr_message` (`message_id`),
    INDEX `idx_mcr_session` (`session_id`),
    INDEX `idx_mcr_chunk` (`chunk_id`),
    INDEX `idx_mcr_knowledge_base` (`knowledge_base_id`),
    INDEX `idx_mcr_tenant` (`tenant_id`, `deleted_at`),
    INDEX `idx_mcr_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='问答回复与知识库片段的关联关系表';

-- ============================================================================
-- 3. 创建用户评价记录表 (chunk_feedbacks)
-- ============================================================================
CREATE TABLE IF NOT EXISTS `chunk_feedbacks` (
    `id` VARCHAR(36) PRIMARY KEY,
    `tenant_id` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `user_id` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '评价用户ID',
    `session_id` VARCHAR(36) NOT NULL COMMENT '所属会话ID',
    `message_id` VARCHAR(36) NOT NULL COMMENT '被评价的回复消息ID',
    `feedback_type` VARCHAR(20) NOT NULL COMMENT '评价类型: like/dislike/unlike/undislike',
    `dislike_reason` VARCHAR(50) DEFAULT NULL COMMENT '点踩原因: inaccurate/incomplete/irrelevant/other',
    `dislike_reason_detail` TEXT DEFAULT NULL COMMENT '用户填写的详细原因',
    `created_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    `updated_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    `deleted_at` DATETIME DEFAULT NULL,
    INDEX `idx_cf_user` (`user_id`),
    INDEX `idx_cf_session` (`session_id`),
    INDEX `idx_cf_message` (`message_id`),
    INDEX `idx_cf_tenant` (`tenant_id`, `deleted_at`),
    INDEX `idx_cf_feedback_type` (`feedback_type`),
    INDEX `idx_cf_created` (`created_at`),
    -- 每个用户对每条消息只能有一条有效评价记录
    UNIQUE INDEX `idx_cf_user_message_unique` (`user_id`, `message_id`, `deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户对问答回复的评价记录表';

-- ============================================================================
-- 4. 创建片段权重变更日志表 (chunk_weight_logs)
-- ============================================================================
CREATE TABLE IF NOT EXISTS `chunk_weight_logs` (
    `id` VARCHAR(36) PRIMARY KEY,
    `tenant_id` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `chunk_id` VARCHAR(36) NOT NULL COMMENT '被调整的片段ID',
    `knowledge_base_id` VARCHAR(36) NOT NULL COMMENT '所属知识库ID',
    `trigger_type` VARCHAR(30) NOT NULL COMMENT '触发类型: user_feedback/auto_adjust/manual_reset/batch_update',
    `trigger_reason` VARCHAR(200) DEFAULT NULL COMMENT '触发原因描述',
    `old_weight` DECIMAL(5,2) NOT NULL COMMENT '调整前权重',
    `new_weight` DECIMAL(5,2) NOT NULL COMMENT '调整后权重',
    `old_like_rate` DECIMAL(5,4) DEFAULT NULL COMMENT '调整时的好评率',
    `new_like_rate` DECIMAL(5,4) DEFAULT NULL COMMENT '调整后的好评率',
    `feedback_id` VARCHAR(36) DEFAULT NULL COMMENT '触发本次调整的评价记录ID (当触发类型为user_feedback时)',
    `operator_id` VARCHAR(64) DEFAULT NULL COMMENT '操作人ID (当触发类型为manual时)',
    `created_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX `idx_cwl_chunk` (`chunk_id`),
    INDEX `idx_cwl_knowledge_base` (`knowledge_base_id`),
    INDEX `idx_cwl_tenant` (`tenant_id`),
    INDEX `idx_cwl_trigger_type` (`trigger_type`),
    INDEX `idx_cwl_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='片段权重变更日志表';

-- ============================================================================
-- 5. 创建系统配置表用于存储阈值配置
-- ============================================================================
CREATE TABLE IF NOT EXISTS `chunk_feedback_config` (
    `id` VARCHAR(36) PRIMARY KEY,
    `tenant_id` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `config_key` VARCHAR(50) NOT NULL COMMENT '配置项名称',
    `config_value` VARCHAR(200) NOT NULL COMMENT '配置值',
    `description` VARCHAR(200) DEFAULT NULL COMMENT '配置说明',
    `updated_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE INDEX `idx_cfc_key` (`tenant_id`, `config_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='片段反馈系统配置表';

-- 插入默认配置值
INSERT IGNORE INTO `chunk_feedback_config` (`id`, `tenant_id`, `config_key`, `config_value`, `description`) VALUES
    (UUID(), 0, 'like_rate_high_threshold', '0.80', '好评率高阈值, 超过此值提升权重'),
    (UUID(), 0, 'like_rate_low_threshold', '0.50', '好评率低阈值, 低于此值降低权重'),
    (UUID(), 0, 'like_rate_optimize_threshold', '0.30', '待优化阈值, 低于此值标记待优化'),
    (UUID(), 0, 'weight_boost_factor', '1.20', '好评时权重提升系数'),
    (UUID(), 0, 'weight_penalty_factor', '0.80', '差评时权重降低系数'),
    (UUID(), 0, 'weight_min', '0.10', '权重最小值'),
    (UUID(), 0, 'weight_max', '2.00', '权重最大值'),
    (UUID(), 0, 'min_feedback_count', '5', '触发权重调整的最小评价数');
